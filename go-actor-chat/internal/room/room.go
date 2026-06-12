package room

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthdm/hollywood/actor"
)

const (
	convexTimeout = 5 * time.Second
	idleTimeout   = 5 * time.Minute
	idleCheckTick = time.Minute
	seenCapacity  = 1024
)

// idleTick is sent to the actor on a repeater so it can self-poison after
// idleTimeout with no activity (PRD §9).
type idleTick struct{}

// Actor is the single writer for one room (PRD §9, §12). It processes its
// mailbox serially, which guarantees in-room message ordering, and keeps a
// bounded idempotency cache so retries are acked without re-hitting Convex.
type Actor struct {
	roomID       string
	store        Store
	seen         *seenCache
	lastActivity time.Time
	repeater     actor.SendRepeater
}

// NewActor returns a Producer for the room actor of the given room.
func NewActor(roomID string, store Store) actor.Producer {
	return func() actor.Receiver {
		return &Actor{
			roomID: roomID,
			store:  store,
			seen:   newSeenCache(seenCapacity),
		}
	}
}

func (a *Actor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		a.lastActivity = time.Now()
		a.repeater = c.SendRepeat(c.PID(), idleTick{}, idleCheckTick)
		slog.Info("room actor started", "room_id", a.roomID, "pid", c.PID())
	case actor.Stopped:
		a.repeater.Stop()
		slog.Info("room actor stopped", "room_id", a.roomID)
	case Send:
		a.lastActivity = time.Now()
		a.handleSend(c, msg)
	case idleTick:
		if time.Since(a.lastActivity) > idleTimeout {
			slog.Info("room actor idle, self-poisoning", "room_id", a.roomID)
			c.Engine().Poison(c.PID())
		}
	}
}

func (a *Actor) handleSend(c *actor.Context, msg Send) {
	// Fast path: this clientId was already persisted by this actor.
	if ack, ok := a.seen.get(msg.ClientID); ok {
		c.Respond(ack)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), convexTimeout)
	defer cancel()

	persisted, err := a.store.SendMessage(ctx, a.roomID, msg.Body, msg.ClientID, msg.Token)
	if err != nil {
		slog.Error("room send failed", "room_id", a.roomID, "client_id", msg.ClientID, "err", err)
		c.Respond(SendError{RoomID: a.roomID, ClientID: msg.ClientID, Reason: err.Error()})
		return
	}

	ack := SendAck{
		RoomID:    a.roomID,
		MessageID: persisted.ID,
		ClientID:  msg.ClientID,
		CreatedAt: persisted.CreatedAt,
	}
	a.seen.put(msg.ClientID, ack)
	c.Respond(ack)
}

// seenCache is a bounded FIFO cache of clientId → SendAck. Losing entries is
// safe: Convex's by_room_clientId index is the authoritative dedupe
// (PRD §10.5); this cache only saves a round trip.
type seenCache struct {
	capacity int
	order    []string
	entries  map[string]SendAck
}

func newSeenCache(capacity int) *seenCache {
	return &seenCache{
		capacity: capacity,
		entries:  make(map[string]SendAck, capacity),
	}
}

func (s *seenCache) get(clientID string) (SendAck, bool) {
	ack, ok := s.entries[clientID]
	return ack, ok
}

func (s *seenCache) put(clientID string, ack SendAck) {
	if _, exists := s.entries[clientID]; exists {
		s.entries[clientID] = ack
		return
	}
	if len(s.order) >= s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
	s.order = append(s.order, clientID)
	s.entries[clientID] = ack
}
