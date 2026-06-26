package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/borbert/actor-chat/go-actor-chat/internal/room"
	"github.com/borbert/actor-chat/go-actor-chat/internal/ws"
)

type fakeStore struct{}

func (fakeStore) SendMessage(_ context.Context, roomID, body, clientID, token string) (room.Message, error) {
	return room.Message{ID: "msg1", ClientID: clientID, CreatedAt: 99}, nil
}

// testAuth is an in-test Clerk stand-in: an RSA keypair, a JWKS server
// publishing the public key, and a token mint. The validator under test
// fetches the JWKS exactly as it would from Clerk.
type testAuth struct {
	key       *rsa.PrivateKey
	issuer    string
	validator *TokenValidator
}

func newTestAuth(t *testing.T) *testAuth {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}

	b64 := base64.RawURLEncoding
	jwks := map[string]any{
		"keys": []map[string]any{{
			"use": "sig",
			"kty": "RSA",
			"kid": "test-key",
			"alg": "RS256",
			"n":   b64.EncodeToString(key.N.Bytes()),
			"e":   b64.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	validator, err := NewTokenValidator(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("validator: %v", err)
	}
	return &testAuth{key: key, issuer: srv.URL, validator: validator}
}

// mint issues a token like the Clerk "convex" template would.
func (a *testAuth) mint(t *testing.T, sub string, ttl time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": a.issuer,
		"aud": "convex",
		"sub": sub,
		"iat": time.Now().Add(-time.Minute).Unix(),
		"exp": time.Now().Add(ttl).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(a.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func newWSTestServer(t *testing.T) (*httptest.Server, *testAuth) {
	t.Helper()
	eng, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	auth := newTestAuth(t)
	e := echo.New()
	s := &Server{echo: e, engine: eng, rooms: room.NewRegistry(eng, fakeStore{}), auth: auth.validator}
	// Stub resolver: derive a fake Convex id from the validated sub, no Convex.
	s.resolveUser = func(_ context.Context, token string) (string, error) {
		sub, err := auth.validator.Validate(token)
		return "cv_" + sub, err
	}
	s.RegisterRoutes()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv, auth
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

func wsStatus(t *testing.T, srv *httptest.Server, query string) int {
	t.Helper()
	resp, err := http.Get(srv.URL + "/ws" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestWSRejectsBadTokens(t *testing.T) {
	srv, auth := newWSTestServer(t)

	t.Run("missing token", func(t *testing.T) {
		if got := wsStatus(t, srv, ""); got != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", got)
		}
	})
	t.Run("garbage token", func(t *testing.T) {
		if got := wsStatus(t, srv, "?token=garbage"); got != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", got)
		}
	})
	t.Run("expired token", func(t *testing.T) {
		tok := auth.mint(t, "u1", -time.Minute)
		if got := wsStatus(t, srv, "?token="+tok); got != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", got)
		}
	})
}

func TestWSPingPong(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))

	writeFrame(t, conn, ws.Frame{Type: ws.TypePing})
	if got := readFrame(t, conn); got.Type != ws.TypePong {
		t.Errorf("type = %q, want pong", got.Type)
	}
}

func TestWSTokenRefresh(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))

	t.Run("same subject accepted", func(t *testing.T) {
		writeFrame(t, conn, ws.Frame{Type: ws.TypePing, Token: auth.mint(t, "u1", time.Minute)})
		if got := readFrame(t, conn); got.Type != ws.TypePong {
			t.Errorf("type = %q, want pong", got.Type)
		}
	})
	t.Run("different subject rejected", func(t *testing.T) {
		writeFrame(t, conn, ws.Frame{Type: ws.TypePing, Token: auth.mint(t, "intruder", time.Minute)})
		if got := readFrame(t, conn); got.Type != ws.TypeError {
			t.Errorf("type = %q, want error", got.Type)
		}
		// Connection survives the rejected refresh: pong still arrives.
		if got := readFrame(t, conn); got.Type != ws.TypePong {
			t.Errorf("type = %q, want pong", got.Type)
		}
	})
}

func TestWSSendAck(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))

	writeFrame(t, conn, ws.Frame{Type: ws.TypeSend, RoomID: "r1", Body: "hello", ClientID: "c1"})
	got := readFrame(t, conn)
	if got.Type != ws.TypeAck {
		t.Fatalf("type = %q, want ack (reason: %s)", got.Type, got.Reason)
	}
	if got.MessageID != "msg1" || got.ClientID != "c1" || got.RoomID != "r1" {
		t.Errorf("unexpected ack: %+v", got)
	}
}

// readUntil reads frames from conn until match returns true or the deadline
// passes. Presence/typing broadcasts can be preceded by intermediate frames,
// so tests read until the frame they want rather than asserting on the first.
func readUntil(t *testing.T, conn *websocket.Conn, match func(ws.Frame) bool) ws.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var f ws.Frame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if match(f) {
			return f
		}
	}
}

func presenceIs(roomID string, users ...string) func(ws.Frame) bool {
	return func(f ws.Frame) bool {
		return f.Type == ws.TypePresenceUpdate && f.RoomID == roomID && slices.Equal(f.Users, users)
	}
}

func TestWSPresenceAndTyping(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn1 := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))
	conn2 := dialWS(t, srv, "?token="+auth.mint(t, "u2", time.Minute))

	writeFrame(t, conn1, ws.Frame{Type: ws.TypeJoin, RoomID: "r1"})
	writeFrame(t, conn2, ws.Frame{Type: ws.TypeJoin, RoomID: "r1"})

	// Both eventually see the full online set (drains queued presence frames).
	readUntil(t, conn1, presenceIs("r1", "cv_u1", "cv_u2"))
	readUntil(t, conn2, presenceIs("r1", "cv_u1", "cv_u2"))

	// u1 starts typing → u2 sees it.
	writeFrame(t, conn1, ws.Frame{Type: ws.TypeTypingStart, RoomID: "r1"})
	got := readUntil(t, conn2, func(f ws.Frame) bool { return f.Type == ws.TypeTypingUpdate })
	if got.UserID != "cv_u1" || !got.Typing {
		t.Errorf("typing update = %+v, want userId cv_u1 typing true", got)
	}

	// Not echoed to sender: conn1's next frame is the pong, not a typing frame.
	writeFrame(t, conn1, ws.Frame{Type: ws.TypePing})
	if got := readFrame(t, conn1); got.Type != ws.TypePong {
		t.Errorf("sender's next frame = %q, want pong (typing must not echo to sender)", got.Type)
	}
}

func TestWSLeaveOnClose(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn1 := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))
	conn2 := dialWS(t, srv, "?token="+auth.mint(t, "u2", time.Minute))

	writeFrame(t, conn1, ws.Frame{Type: ws.TypeJoin, RoomID: "r1"})
	writeFrame(t, conn2, ws.Frame{Type: ws.TypeJoin, RoomID: "r1"})
	readUntil(t, conn2, presenceIs("r1", "cv_u1", "cv_u2"))

	// conn1 drops → its actor.Stopped mails Leave, so conn2 sees u1 gone.
	conn1.Close(websocket.StatusNormalClosure, "")
	readUntil(t, conn2, presenceIs("r1", "cv_u2"))
}

func TestWSSendValidation(t *testing.T) {
	srv, auth := newWSTestServer(t)
	conn := dialWS(t, srv, "?token="+auth.mint(t, "u1", time.Minute))

	writeFrame(t, conn, ws.Frame{Type: ws.TypeSend, RoomID: "r1"})
	got := readFrame(t, conn)
	if got.Type != ws.TypeError {
		t.Errorf("type = %q, want error", got.Type)
	}
}
