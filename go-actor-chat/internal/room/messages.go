package room

// Actor message types for the room actor (PRD §9). Join/Leave/Typing are
// added in M4 alongside presence.

// Send asks a room actor to validate and persist one chat message. ClientID
// is the sender-generated idempotency key (PRD §12).
type Send struct {
	UserID   string
	Body     string
	ClientID string
}

// SendAck confirms that a message was persisted (or already existed).
type SendAck struct {
	RoomID    string
	MessageID string
	ClientID  string
	CreatedAt float64
}

// SendError reports a failed send back to the requester.
type SendError struct {
	RoomID   string
	ClientID string
	Reason   string
}
