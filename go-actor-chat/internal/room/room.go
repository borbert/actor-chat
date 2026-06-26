package room

import (
	"context"
	"log/slog"
	"sort"
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

// member is one connected tab: where to mail updates, and who it is.
type member struct {
	pid    *actor.PID
	userID string
}

// Actor is the single writer for one room (PRD §9, §12). It processes its
// mailbox serially, which guarantees in-room message ordering, and keeps a
// bounded idempotency cache so retries are acked without re-hitting Convex.
//
// It also owns the room's ephemeral presence: who is connected and who is
// typing. This state lives only here, in memory — when the actor dies it
// dies with it, which is correct (a restarted server has no connections).
type Actor struct {
	roomID       string
	store        Store
	seen         *seenCache
	lastActivity time.Time
	repeater     actor.SendRepeater
	members      map[string]member // key: connection PID string (one user may have many tabs)
	typing       map[string]bool   // userIDs currently typing
}

// NewActor returns a Producer for the room actor of the given room.
func NewActor(roomID string, store Store) actor.Producer {
	return func() actor.Receiver {
		return &Actor{
			roomID:  roomID,
			store:   store,
			seen:    newSeenCache(seenCapacity),
			members: make(map[string]member),
			typing:  make(map[string]bool),
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
	case Join:
		a.lastActivity = time.Now()
		a.handleJoin(c, msg)
	case Leave:
		a.lastActivity = time.Now()
		a.handleLeave(c)
	case Typing:
		a.lastActivity = time.Now()
		a.handleTyping(c, msg)
	case idleTick:
		// An occupied room holds presence state that must not evaporate under
		// a quiet lurker, so it only self-poisons when empty *and* idle.
		if len(a.members) == 0 && time.Since(a.lastActivity) > idleTimeout {
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

// handleJoin records the sending connection as a member and broadcasts the
// new online set. The broadcast goes to everyone including the joiner, so
// the joiner gets its initial presence snapshot with no separate sync reply.
func (a *Actor) handleJoin(c *actor.Context, msg Join) {
	sender := c.Sender()
	if sender == nil {
		return
	}
	a.members[sender.String()] = member{pid: sender, userID: msg.UserID}
	a.broadcastPresence(c)
}

// handleLeave drops the sending connection. If that was the user's last tab,
// their typing state must not linger, so it is cleared and broadcast too.
func (a *Actor) handleLeave(c *actor.Context) {
	sender := c.Sender()
	if sender == nil {
		return
	}
	key := sender.String()
	m, ok := a.members[key]
	if !ok {
		return
	}
	delete(a.members, key)
	if a.typing[m.userID] && !a.online(m.userID) {
		delete(a.typing, m.userID)
		a.broadcast(c, TypingUpdate{RoomID: a.roomID, UserID: m.userID}, "")
	}
	a.broadcastPresence(c)
}

// handleTyping records the sender's typing state and fans it out to the
// other members. The sender's own tab is skipped (the client filters self).
func (a *Actor) handleTyping(c *actor.Context, msg Typing) {
	if msg.Typing {
		a.typing[msg.UserID] = true
	} else {
		delete(a.typing, msg.UserID)
	}
	exclude := ""
	if s := c.Sender(); s != nil {
		exclude = s.String()
	}
	a.broadcast(c, TypingUpdate{RoomID: a.roomID, UserID: msg.UserID, Typing: msg.Typing}, exclude)
}

// online reports whether any connected tab belongs to userID.
func (a *Actor) online(userID string) bool {
	for _, m := range a.members {
		if m.userID == userID {
			return true
		}
	}
	return false
}

// presence returns the deduplicated, sorted set of online userIDs. Sorted so
// broadcasts are deterministic (and so are tests).
func (a *Actor) presence() []string {
	set := make(map[string]bool, len(a.members))
	for _, m := range a.members {
		set[m.userID] = true
	}
	users := make([]string, 0, len(set))
	for u := range set {
		users = append(users, u)
	}
	sort.Strings(users)
	return users
}

func (a *Actor) broadcastPresence(c *actor.Context) {
	a.broadcast(c, PresenceUpdate{RoomID: a.roomID, Users: a.presence()}, "")
}

// broadcast mails msg to every member except the PID keyed by exclude ("" excludes
// nobody). Fan-out is just a loop over PIDs — each member's connection actor
// serializes the actual socket write, so a broadcast never interleaves mid-frame.
func (a *Actor) broadcast(c *actor.Context, msg any, exclude string) {
	for key, m := range a.members {
		if key == exclude {
			continue
		}
		c.Send(m.pid, msg)
	}
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
