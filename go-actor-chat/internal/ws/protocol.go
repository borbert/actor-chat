package ws

// Frame is the JSON envelope for every message on the app WebSocket
// (PRD §11). One struct covers both directions; unused fields are omitted.
//
// Inbound types:  join, leave, send, typing_start, typing_stop, ping
// Outbound types: ack, error, presence_update, typing_update, pong
type Frame struct {
	Type string `json:"type"`

	// Common
	RoomID string `json:"roomId,omitempty"`
	UserID string `json:"userId,omitempty"`

	// send / ack / error
	Body      string  `json:"body,omitempty"`
	ClientID  string  `json:"clientId,omitempty"`
	MessageID string  `json:"messageId,omitempty"`
	CreatedAt float64 `json:"createdAt,omitempty"`
	Reason    string  `json:"reason,omitempty"`

	// presence_update: full set of online userIds in the room
	Users []string `json:"users,omitempty"`

	// typing_update
	Typing bool `json:"typing,omitempty"`

	// ping: optional fresh JWT so long-lived connections outlive the ~60s
	// token lifetime (PRD §13). Never sent by the server.
	Token string `json:"token,omitempty"`
}

// Inbound frame types.
const (
	TypeJoin        = "join"
	TypeLeave       = "leave"
	TypeSend        = "send"
	TypeTypingStart = "typing_start"
	TypeTypingStop  = "typing_stop"
	TypePing        = "ping"
)

// Outbound frame types.
const (
	TypeAck            = "ack"
	TypeError          = "error"
	TypePresenceUpdate = "presence_update"
	TypeTypingUpdate   = "typing_update"
	TypePong           = "pong"
)
