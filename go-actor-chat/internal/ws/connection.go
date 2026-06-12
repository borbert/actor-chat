package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"

	"github.com/borbert/actor-chat/go-actor-chat/internal/room"
)

const (
	// readTimeout is the WS read deadline, refreshed by any inbound frame
	// including ping (PRD §12).
	readTimeout  = 60 * time.Second
	writeTimeout = 10 * time.Second
)

// malformedFrame tells the connection actor the peer sent unparseable JSON.
// A distinct message type (rather than a sentinel Frame) keeps internal
// signals out of the wire protocol.
type malformedFrame struct{}

// Connection is the actor owning one WebSocket connection (PRD §9). All
// writes to the socket go through its mailbox, so they are serialized; reads
// happen in ReadLoop on the HTTP handler goroutine and are forwarded here as
// Frame messages.
type Connection struct {
	conn     *websocket.Conn
	userID   string
	registry *room.Registry
	joined   map[string]bool
}

// NewConnection returns a Producer for a connection actor bound to conn.
func NewConnection(conn *websocket.Conn, userID string, registry *room.Registry) actor.Producer {
	return func() actor.Receiver {
		return &Connection{
			conn:     conn,
			userID:   userID,
			registry: registry,
			joined:   make(map[string]bool),
		}
	}
}

func (cn *Connection) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		slog.Info("ws connection actor started", "user_id", cn.userID, "pid", c.PID())
	case actor.Stopped:
		// Best effort: the peer may already be gone.
		_ = cn.conn.Close(websocket.StatusNormalClosure, "server closing connection")
		slog.Info("ws connection actor stopped", "user_id", cn.userID)
	case Frame:
		cn.handleFrame(c, msg)
	case malformedFrame:
		cn.write(Frame{Type: TypeError, Reason: "malformed JSON"})
	case room.SendAck:
		cn.write(Frame{
			Type:      TypeAck,
			RoomID:    msg.RoomID,
			MessageID: msg.MessageID,
			ClientID:  msg.ClientID,
			CreatedAt: msg.CreatedAt,
		})
	case room.SendError:
		cn.write(Frame{
			Type:     TypeError,
			RoomID:   msg.RoomID,
			ClientID: msg.ClientID,
			Reason:   msg.Reason,
		})
	}
}

func (cn *Connection) handleFrame(c *actor.Context, f Frame) {
	switch f.Type {
	case TypePing:
		cn.write(Frame{Type: TypePong})
	case TypeJoin:
		if f.RoomID == "" {
			cn.write(Frame{Type: TypeError, Reason: "join requires roomId"})
			return
		}
		cn.joined[f.RoomID] = true
		// M4 adds room.Join so the room actor can track presence.
	case TypeLeave:
		delete(cn.joined, f.RoomID)
		// M4 adds room.Leave.
	case TypeSend:
		if f.RoomID == "" || f.Body == "" || f.ClientID == "" {
			cn.write(Frame{Type: TypeError, ClientID: f.ClientID, Reason: "send requires roomId, body, and clientId"})
			return
		}
		pid := cn.registry.PIDFor(f.RoomID)
		// Sent with this actor as sender; the room actor's Respond comes
		// back to our mailbox as room.SendAck / room.SendError.
		c.Send(pid, room.Send{
			UserID:   cn.userID,
			Body:     f.Body,
			ClientID: f.ClientID,
		})
	case TypeTypingStart, TypeTypingStop:
		// Implemented in M4.
	default:
		cn.write(Frame{Type: TypeError, Reason: "unknown frame type: " + f.Type})
	}
}

func (cn *Connection) write(f Frame) {
	data, err := json.Marshal(f)
	if err != nil {
		slog.Error("ws marshal frame", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := cn.conn.Write(ctx, websocket.MessageText, data); err != nil {
		slog.Warn("ws write failed", "user_id", cn.userID, "err", err)
	}
}

// ReadLoop reads frames from conn and forwards them to the connection actor
// until the connection closes or a read times out. It runs on the HTTP
// handler goroutine so blocking reads stay out of the actor.
func ReadLoop(ctx context.Context, conn *websocket.Conn, engine *actor.Engine, pid *actor.PID) {
	for {
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		typ, data, err := conn.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			// Routed through the actor so the error frame write is
			// serialized with all other writes.
			engine.Send(pid, malformedFrame{})
			continue
		}
		engine.Send(pid, f)
	}
}
