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

// Identity is what the upgrade handler established about the peer. AuthID is
// the fixed proof of who they are — token refreshes must present the same
// Clerk sub — while UserID is the public identity others render (presence and
// typing key off users._id). Token is the current JWT lease, swapped on each
// ping refresh.
type Identity struct {
	UserID string // Convex users._id
	AuthID string // Clerk JWT subject
	Token  string // current JWT lease
}

// Connection is the actor owning one WebSocket connection (PRD §9). All
// writes to the socket go through its mailbox, so they are serialized; reads
// happen in ReadLoop on the HTTP handler goroutine and are forwarded here as
// Frame messages.
//
// id.Token is a lease: Clerk JWTs expire in ~60s while connections live for
// hours, so the client mails a fresh one on each ping and the actor swaps
// its private copy. No shared state, no locks — renewal is just a message.
type Connection struct {
	conn     *websocket.Conn
	id       Identity
	validate func(string) (string, error)
	registry *room.Registry
	joined   map[string]bool
}

// NewConnection returns a Producer for a connection actor bound to conn.
// validate is the server's JWT check (passed as a func to keep this package
// free of an import on the server package); id was already established at
// upgrade.
func NewConnection(conn *websocket.Conn, id Identity, validate func(string) (string, error), registry *room.Registry) actor.Producer {
	return func() actor.Receiver {
		return &Connection{
			conn:     conn,
			id:       id,
			validate: validate,
			registry: registry,
			joined:   make(map[string]bool),
		}
	}
}

func (cn *Connection) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		slog.Info("ws connection actor started", "user_id", cn.id.UserID, "pid", c.PID())
	case actor.Stopped:
		// Leave every joined room before the socket goes — the actor that
		// holds the joined set is best placed to clean up its own presence.
		for roomID := range cn.joined {
			c.Send(cn.registry.PIDFor(roomID), room.Leave{})
		}
		// Best effort: the peer may already be gone.
		_ = cn.conn.Close(websocket.StatusNormalClosure, "server closing connection")
		slog.Info("ws connection actor stopped", "user_id", cn.id.UserID)
	case Frame:
		cn.handleFrame(c, msg)
	case malformedFrame:
		cn.write(Frame{Type: TypeError, Reason: "malformed JSON"})
	case room.PresenceUpdate:
		cn.write(Frame{Type: TypePresenceUpdate, RoomID: msg.RoomID, Users: msg.Users})
	case room.TypingUpdate:
		cn.write(Frame{Type: TypeTypingUpdate, RoomID: msg.RoomID, UserID: msg.UserID, Typing: msg.Typing})
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
		if f.Token != "" {
			// Refresh the lease. Same-subject check: a connection's
			// identity is fixed at upgrade; a token for someone else is
			// an error, not a re-login.
			sub, err := cn.validate(f.Token)
			if err != nil || sub != cn.id.AuthID {
				cn.write(Frame{Type: TypeError, Reason: "token refresh rejected"})
				cn.write(Frame{Type: TypePong})
				return
			}
			cn.id.Token = f.Token
		}
		cn.write(Frame{Type: TypePong})
	case TypeJoin:
		if f.RoomID == "" {
			cn.write(Frame{Type: TypeError, Reason: "join requires roomId"})
			return
		}
		if cn.joined[f.RoomID] {
			return // React effects re-fire; join once
		}
		cn.joined[f.RoomID] = true
		c.Send(cn.registry.PIDFor(f.RoomID), room.Join{UserID: cn.id.UserID})
	case TypeLeave:
		if !cn.joined[f.RoomID] {
			return
		}
		delete(cn.joined, f.RoomID)
		c.Send(cn.registry.PIDFor(f.RoomID), room.Leave{})
	case TypeSend:
		if f.RoomID == "" || f.Body == "" || f.ClientID == "" {
			cn.write(Frame{Type: TypeError, ClientID: f.ClientID, Reason: "send requires roomId, body, and clientId"})
			return
		}
		pid := cn.registry.PIDFor(f.RoomID)
		// Sent with this actor as sender; the room actor's Respond comes
		// back to our mailbox as room.SendAck / room.SendError.
		c.Send(pid, room.Send{
			Body:     f.Body,
			ClientID: f.ClientID,
			Token:    cn.id.Token,
		})
	case TypeTypingStart, TypeTypingStop:
		if !cn.joined[f.RoomID] {
			return // not a member: nothing to fan out, not worth an error
		}
		c.Send(cn.registry.PIDFor(f.RoomID), room.Typing{
			UserID: cn.id.UserID,
			Typing: f.Type == TypeTypingStart,
		})
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
		slog.Warn("ws write failed", "user_id", cn.id.UserID, "err", err)
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
