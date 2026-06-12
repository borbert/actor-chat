package room

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
)

// stubStore records calls and returns canned results, so actor behavior can
// be tested without a Convex deployment.
type stubStore struct {
	mu    sync.Mutex
	calls []string // clientIds in call order
	err   error
}

func (s *stubStore) SendMessage(_ context.Context, roomID, userID, body, clientID string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, clientID)
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

			res := sendAndWait(t, eng, pid, Send{UserID: "u1", Body: "hi", ClientID: "c1"})

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

	first := sendAndWait(t, eng, pid, Send{UserID: "u1", Body: "hi", ClientID: "dup"})
	second := sendAndWait(t, eng, pid, Send{UserID: "u1", Body: "hi", ClientID: "dup"})

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
