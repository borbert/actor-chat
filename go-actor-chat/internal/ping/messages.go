package ping


import (
	_ "github.com/anthdm/hollywood/actor"
	"time"

)

// PingActor is the actor that handles ping messages

// Ping message
type Ping struct {
	Nonce string
}

// Pong message
type Pong struct {
	Nonce string
	At time.Time
}



