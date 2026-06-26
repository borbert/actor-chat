package room

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
)

// stubStore records calls and returns canned results, so actor behavior can
// be tested without a Convex deployment.
type stubStore struct {
	mu     sync.Mutex
	calls  []string // clientIds in call order
	tokens []string // tokens in call order
	err    error
}

func (s *stubStore) SendMessage(_ context.Context, roomID, body, clientID, token string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, clientID)
	s.tokens = append(s.tokens, token)
	if s.err != nil {
		return Message{}, s.err
	}
	return Message{ID: "msg_" + clientID, ClientID: clientID, CreatedAt: 1234}, nil
}

func (s *stubStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func newTestEngine(t *testing.T) *actor.Engine {
	t.Helper()
	eng, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return eng
}

func sendAndWait(t *testing.T, eng *actor.Engine, pid *actor.PID, msg Send) any {
	t.Helper()
	resp := eng.Request(pid, msg, 2*time.Second)
	res, err := resp.Result()
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return res
}

func TestRoomActorSend(t *testing.T) {
	tests := []struct {
		name      string
		storeErr  error
		wantAck   bool
		wantMsgID string
	}{
		{name: "persists and acks", wantAck: true, wantMsgID: "msg_c1"},
		{name: "store failure returns SendError", storeErr: errors.New("convex down"), wantAck: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := newTestEngine(t)
			store := &stubStore{err: tt.storeErr}
			pid := eng.Spawn(NewActor("r1", store), actorKind, actor.WithID("r1"))

			res := sendAndWait(t, eng, pid, Send{Body: "hi", ClientID: "c1", Token: "tok1"})

			if tt.wantAck {
				ack, ok := res.(SendAck)
				if !ok {
					t.Fatalf("expected SendAck, got %T", res)
				}
				if ack.MessageID != tt.wantMsgID {
					t.Errorf("messageId = %q, want %q", ack.MessageID, tt.wantMsgID)
				}
				if ack.ClientID != "c1" {
					t.Errorf("clientId = %q, want c1", ack.ClientID)
				}
				// The sender's token must reach the store: that is what
				// makes the Convex write run as the user (PRD §13).
				store.mu.Lock()
				gotToken := store.tokens[0]
				store.mu.Unlock()
				if gotToken != "tok1" {
					t.Errorf("store token = %q, want tok1", gotToken)
				}
			} else {
				sendErr, ok := res.(SendError)
				if !ok {
					t.Fatalf("expected SendError, got %T", res)
				}
				if sendErr.ClientID != "c1" {
					t.Errorf("clientId = %q, want c1", sendErr.ClientID)
				}
			}
		})
	}
}

func TestRoomActorIdempotency(t *testing.T) {
	eng := newTestEngine(t)
	store := &stubStore{}
	pid := eng.Spawn(NewActor("r1", store), actorKind, actor.WithID("r1"))

	first := sendAndWait(t, eng, pid, Send{Body: "hi", ClientID: "dup"})
	second := sendAndWait(t, eng, pid, Send{Body: "hi", ClientID: "dup"})

	firstAck, ok := first.(SendAck)
	if !ok {
		t.Fatalf("expected SendAck, got %T", first)
	}
	secondAck, ok := second.(SendAck)
	if !ok {
		t.Fatalf("expected SendAck, got %T", second)
	}
	if firstAck != secondAck {
		t.Errorf("acks differ: %+v vs %+v", firstAck, secondAck)
	}
	if got := store.callCount(); got != 1 {
		t.Errorf("store called %d times, want 1 (duplicate must hit the seen cache)", got)
	}
}

// probe records every message it receives, standing in for a connection
// actor so the room actor has a real sender PID to broadcast back to.
type probe struct {
	mu  sync.Mutex
	got []any
}

func (p *probe) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started, actor.Stopped, actor.Initialized:
		return
	}
	p.mu.Lock()
	p.got = append(p.got, c.Message())
	p.mu.Unlock()
}

func (p *probe) snapshot() []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]any(nil), p.got...)
}

// lastPresence returns the Users of the last PresenceUpdate the probe saw,
// or nil if it has seen none.
func (p *probe) lastPresence() ([]string, bool) {
	var users []string
	var seen bool
	for _, m := range p.snapshot() {
		if pu, ok := m.(PresenceUpdate); ok {
			users, seen = pu.Users, true
		}
	}
	return users, seen
}

func (p *probe) typingUpdates() []TypingUpdate {
	var out []TypingUpdate
	for _, m := range p.snapshot() {
		if tu, ok := m.(TypingUpdate); ok {
			out = append(out, tu)
		}
	}
	return out
}

// waitFor polls cond until it is true or the deadline passes. Broadcasts are
// async, so assertions must wait rather than read once.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestRoomPresenceJoin(t *testing.T) {
	eng := newTestEngine(t)
	roomPID := eng.Spawn(NewActor("r1", &stubStore{}), actorKind, actor.WithID("r1"))

	p1, p2 := &probe{}, &probe{}
	pid1 := eng.Spawn(func() actor.Receiver { return p1 }, "probe", actor.WithID("p1"))
	pid2 := eng.Spawn(func() actor.Receiver { return p2 }, "probe", actor.WithID("p2"))

	eng.SendWithSender(roomPID, Join{UserID: "cv1"}, pid1)
	eng.SendWithSender(roomPID, Join{UserID: "cv2"}, pid2)

	want := []string{"cv1", "cv2"}
	for _, p := range []*probe{p1, p2} {
		waitFor(t, func() bool {
			users, ok := p.lastPresence()
			return ok && slices.Equal(users, want)
		})
	}
}

func TestRoomPresenceMultipleTabs(t *testing.T) {
	eng := newTestEngine(t)
	roomPID := eng.Spawn(NewActor("r1", &stubStore{}), actorKind, actor.WithID("r1"))

	p1, p2 := &probe{}, &probe{}
	pid1 := eng.Spawn(func() actor.Receiver { return p1 }, "probe", actor.WithID("p1"))
	pid2 := eng.Spawn(func() actor.Receiver { return p2 }, "probe", actor.WithID("p2"))

	// Same user, two tabs.
	eng.SendWithSender(roomPID, Join{UserID: "cv1"}, pid1)
	eng.SendWithSender(roomPID, Join{UserID: "cv1"}, pid2)

	// Presence lists the user once.
	waitFor(t, func() bool {
		users, ok := p1.lastPresence()
		return ok && slices.Equal(users, []string{"cv1"})
	})

	// One tab leaves; the user is still present via the other.
	eng.SendWithSender(roomPID, Leave{}, pid1)
	waitFor(t, func() bool {
		users, ok := p2.lastPresence()
		return ok && slices.Equal(users, []string{"cv1"})
	})
}

func TestRoomTypingNotEchoedToSender(t *testing.T) {
	eng := newTestEngine(t)
	roomPID := eng.Spawn(NewActor("r1", &stubStore{}), actorKind, actor.WithID("r1"))

	pa, pb := &probe{}, &probe{}
	pidA := eng.Spawn(func() actor.Receiver { return pa }, "probe", actor.WithID("pa"))
	pidB := eng.Spawn(func() actor.Receiver { return pb }, "probe", actor.WithID("pb"))

	eng.SendWithSender(roomPID, Join{UserID: "cvA"}, pidA)
	eng.SendWithSender(roomPID, Join{UserID: "cvB"}, pidB)

	eng.SendWithSender(roomPID, Typing{UserID: "cvA", Typing: true}, pidA)

	// B receives A's typing update...
	waitFor(t, func() bool {
		for _, tu := range pb.typingUpdates() {
			if tu.UserID == "cvA" && tu.Typing {
				return true
			}
		}
		return false
	})
	// ...and A does not see its own.
	for _, tu := range pa.typingUpdates() {
		if tu.UserID == "cvA" {
			t.Errorf("sender saw its own typing update: %+v", tu)
		}
	}
}

func TestRoomLeaveWhileTyping(t *testing.T) {
	eng := newTestEngine(t)
	roomPID := eng.Spawn(NewActor("r1", &stubStore{}), actorKind, actor.WithID("r1"))

	pa, pb := &probe{}, &probe{}
	pidA := eng.Spawn(func() actor.Receiver { return pa }, "probe", actor.WithID("pa"))
	pidB := eng.Spawn(func() actor.Receiver { return pb }, "probe", actor.WithID("pb"))

	eng.SendWithSender(roomPID, Join{UserID: "cvA"}, pidA)
	eng.SendWithSender(roomPID, Join{UserID: "cvB"}, pidB)
	eng.SendWithSender(roomPID, Typing{UserID: "cvA", Typing: true}, pidA)
	eng.SendWithSender(roomPID, Leave{}, pidA)

	// B sees A's typing cleared (Typing: false) and A gone from presence.
	waitFor(t, func() bool {
		clearedTyping := false
		for _, tu := range pb.typingUpdates() {
			if tu.UserID == "cvA" && !tu.Typing {
				clearedTyping = true
			}
		}
		users, ok := pb.lastPresence()
		return clearedTyping && ok && slices.Equal(users, []string{"cvB"})
	})
}

func TestSeenCacheBounded(t *testing.T) {
	cache := newSeenCache(2)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("c%d", i)
		cache.put(id, SendAck{ClientID: id})
	}
	if _, ok := cache.get("c0"); ok {
		t.Error("oldest entry c0 should have been evicted")
	}
	for _, id := range []string{"c1", "c2"} {
		if _, ok := cache.get(id); !ok {
			t.Errorf("entry %s should still be cached", id)
		}
	}
}
