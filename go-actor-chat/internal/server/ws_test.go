package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"

	"github.com/borbert/actor-chat/go-actor-chat/internal/room"
	"github.com/borbert/actor-chat/go-actor-chat/internal/ws"
)

type fakeStore struct{}

func (fakeStore) SendMessage(_ context.Context, roomID, userID, body, clientID string) (room.Message, error) {
	return room.Message{ID: "msg1", ClientID: clientID, CreatedAt: 99}, nil
}

func newWSTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	eng, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	e := echo.New()
	s := &Server{echo: e, engine: eng, rooms: room.NewRegistry(eng, fakeStore{})}
	s.RegisterRoutes()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv
}

func dialWS(t *testing.T, srv *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/ws" + query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func writeFrame(t *testing.T, conn *websocket.Conn, f ws.Frame) {
	t.Helper()
	data, _ := json.Marshal(f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) ws.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f ws.Frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return f
}

func TestWSRequiresIdentity(t *testing.T) {
	srv := newWSTestServer(t)
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWSPingPong(t *testing.T) {
	srv := newWSTestServer(t)
	conn := dialWS(t, srv, "?user=u1")

	writeFrame(t, conn, ws.Frame{Type: ws.TypePing})
	if got := readFrame(t, conn); got.Type != ws.TypePong {
		t.Errorf("type = %q, want pong", got.Type)
	}
}

func TestWSSendAck(t *testing.T) {
	srv := newWSTestServer(t)
	conn := dialWS(t, srv, "?user=u1")

	writeFrame(t, conn, ws.Frame{Type: ws.TypeSend, RoomID: "r1", Body: "hello", ClientID: "c1"})
	got := readFrame(t, conn)
	if got.Type != ws.TypeAck {
		t.Fatalf("type = %q, want ack (reason: %s)", got.Type, got.Reason)
	}
	if got.MessageID != "msg1" || got.ClientID != "c1" || got.RoomID != "r1" {
		t.Errorf("unexpected ack: %+v", got)
	}
}

func TestWSSendValidation(t *testing.T) {
	srv := newWSTestServer(t)
	conn := dialWS(t, srv, "?user=u1")

	writeFrame(t, conn, ws.Frame{Type: ws.TypeSend, RoomID: "r1"})
	got := readFrame(t, conn)
	if got.Type != ws.TypeError {
		t.Errorf("type = %q, want error", got.Type)
	}
}
