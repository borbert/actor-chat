package room

import (
	"context"
	"fmt"

	"github.com/borbert/actor-chat/go-actor-chat/internal/convex"
)

// Message is the persisted view of a chat message that the actor cares about.
type Message struct {
	ID        string  `json:"_id"`
	ClientID  string  `json:"clientId"`
	CreatedAt float64 `json:"createdAt"`
}

// Store persists chat messages durably. Defined here (at the consumer) so
// tests can stub it without a real Convex deployment. token is the sender's
// bearer JWT; the backend derives the writing user from it.
type Store interface {
	SendMessage(ctx context.Context, roomID, body, clientID, token string) (Message, error)
}

// ConvexStore implements Store on top of the Convex HTTP client by calling
// the messages:send mutation.
type ConvexStore struct {
	client *convex.Client
}

func NewConvexStore(client *convex.Client) *ConvexStore {
	return &ConvexStore{client: client}
}

func (s *ConvexStore) SendMessage(ctx context.Context, roomID, body, clientID, token string) (Message, error) {
	var msg Message
	// No userId arg: messages:send derives the sender from the JWT and
	// rejects unknown args.
	args := map[string]any{
		"roomId":   roomID,
		"body":     body,
		"clientId": clientID,
	}
	if err := s.client.WithAuth(token).Mutation(ctx, "messages:send", args, &msg); err != nil {
		return Message{}, fmt.Errorf("messages:send: %w", err)
	}
	return msg, nil
}
