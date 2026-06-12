package ping

import "time"

// Ping asks the ping actor for a liveness reply.
type Ping struct {
	Nonce string
}

// Pong is the reply to a Ping, echoing the nonce.
type Pong struct {
	Nonce string
	At    time.Time
}
