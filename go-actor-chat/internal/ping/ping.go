package ping

import (
	"log/slog"
	"time"
	"github.com/anthdm/hollywood/actor"
)


type Actor struct {}

func New() actor.Receiver {
	return &Actor{}
}

func (a *Actor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	// no explicit default case, so unknown types fall through
	case actor.Started:
		slog.Info("ping actor started", "pid", c.PID())
	case Ping:
		c.Respond(Pong{At: time.Now(), Nonce: msg.Nonce})

}
}
