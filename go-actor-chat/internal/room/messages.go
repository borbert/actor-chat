package room

// Actor message types for the room actor (PRD §9, §11).

// Send asks a room actor to validate and persist one chat message. ClientID
// is the sender-generated idempotency key (PRD §12). Token is the sender's
// bearer JWT; the Convex mutation derives identity from it (PRD §13), so
// the write executes as the user even though the actor is the single writer.
type Send struct {
	Body     string
	ClientID string
	Token    string
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

// Join registers the sending connection actor as a member of the room.
// UserID is the Convex users._id — the identity other clients render.
type Join struct {
	UserID string
}

// Leave removes the sending connection from the room. No UserID: members
// are keyed by connection PID, because one user may have several tabs.
type Leave struct{}

// Typing reports the sender's typing state for fan-out to other members.
type Typing struct {
	UserID string
	Typing bool
}

// PresenceUpdate is broadcast to every member when the online set changes.
// Users is always the full set (PRD §11): idempotent for the client, no
// diff protocol to get wrong.
type PresenceUpdate struct {
	RoomID string
	Users  []string
}

// TypingUpdate fans out one member's typing state to the other members.
type TypingUpdate struct {
	RoomID string
	UserID string
	Typing bool
}
