# M3 (server half): Clerk JWT auth on the Go WebSocket

Goal (PRD §13, §10.3): the WS upgrade validates a Clerk JWT, the sender's
token rides along to Convex so mutations run as that user, and the dead send
path comes back to life. When this lands, the socket dot goes green and
messages flow end to end.

## The actor-model frame

Three ideas from this milestone worth internalizing:

1. **Validate at the boundary, then trust the message.** The HTTP handler is
   the only place that sees "the outside." Once it validates the JWT, identity
   becomes plain data on actor messages (`Send.Token`). No actor re-checks
   who you are — they can't even reach the network-facing code.
2. **Tokens are leases, not facts.** A Clerk template token expires in ~60s;
   a connection lives for hours. So the ConnectionActor holds the *current*
   token as private state, and the client mails it a fresh one periodically.
   State updates arrive as messages — never shared, never locked.
3. **Per-request auth is just data threading.** The Convex client doesn't
   become "the user's client"; each call carries the bearer token of whoever
   caused it. The RoomActor stays a single writer for ordering, while writes
   execute under different identities.

## Design decision: token refresh over ping

Clerk's `convex` JWT template tokens default to a 60-second lifetime. The
client already pings every 25s to keep the read deadline alive — we piggyback
a fresh token on that same frame: `{type: "ping", token: "<jwt>"}`. The
ConnectionActor revalidates it (cheap, local RSA verify; JWKS is cached) and
swaps its stored token. Every `send` then carries a token that is at most
~25s old.

Optional dashboard knob: the template's token lifetime can be raised (e.g.
120s) for slack, but the refresh design works at 60s.

Known v1 tradeoff: the token travels in the upgrade query string, which
Echo's logger will print. Acceptable on localhost; M5 hardening either
strips the query from logs or moves the token to a `Sec-WebSocket-Protocol`
entry.

---

## 1. Dependencies

```bash
go get github.com/golang-jwt/jwt/v5 github.com/MicahParks/keyfunc/v3
```

## 2. Environment

Root `.env.local` (the Go server's env), add:

```bash
CLERK_JWT_ISSUER_DOMAIN=https://actual-grouper-17.clerk.accounts.dev
```

Same value the Convex deployment already has. JWKS is derived from it:
`<issuer>/.well-known/jwks.json` (verified live: RS256, kid-keyed).

## 3. New file: `internal/server/auth.go`

```go
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// TokenValidator verifies Clerk-issued JWTs against the instance JWKS
// (PRD §13). It is the only component that knows what "authenticated"
// means; everything past the WS upgrade treats identity as message data.
type TokenValidator struct {
	jwks   keyfunc.Keyfunc
	issuer string
}

// NewTokenValidator fetches the JWKS for issuer and keeps it refreshed in
// the background (keyfunc handles caching and kid rotation).
func NewTokenValidator(ctx context.Context, issuer string) (*TokenValidator, error) {
	jwksURL := strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	jwks, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetch jwks %q: %w", jwksURL, err)
	}
	return &TokenValidator{jwks: jwks, issuer: issuer}, nil
}

// Validate checks signature, issuer, audience, and expiry, and returns the
// subject (the Clerk user id, which Convex stores as users.authId).
func (v *TokenValidator) Validate(tokenString string) (string, error) {
	tok, err := jwt.Parse(tokenString, v.jwks.Keyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience("convex"), // set by the "convex" JWT template
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}
	sub, err := tok.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", fmt.Errorf("token has no subject")
	}
	return sub, nil
}
```

## 4. `internal/server/server.go`

- Add field `auth *TokenValidator` to `Server`.
- In `New()`, after the engine is created:

```go
if issuer := os.Getenv("CLERK_JWT_ISSUER_DOMAIN"); issuer != "" {
	validator, err := NewTokenValidator(context.Background(), issuer)
	if err != nil {
		return nil, fmt.Errorf("clerk token validator: %w", err)
	}
	s.auth = validator
}
```

(`context.Background()` is right here: the JWKS cache lives as long as the
process.)

## 5. `internal/server/ws.go` — replace the identity stanza

```go
func (s *Server) wsHandler(c echo.Context) error {
	if s.rooms == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "CONVEX_URL not configured")
	}
	if s.auth == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "CLERK_JWT_ISSUER_DOMAIN not configured")
	}

	token := c.QueryParam("token")
	if token == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing token")
	}
	userID, err := s.auth.Validate(token)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	}
	// ... Accept stays the same ...

	pid := s.engine.Spawn(ws.NewConnection(conn, userID, token, s.auth, s.rooms), "conn")
	// ... rest unchanged ...
}
```

Don't echo the validation error detail to the client (it leaks validator
internals); log it server-side instead.

## 6. `internal/ws/protocol.go`

Add to `Frame`:

```go
	// ping: optional fresh JWT so long-lived connections outlive the
	// ~60s token lifetime. Never sent by the server.
	Token string `json:"token,omitempty"`
```

## 7. `internal/ws/connection.go`

- `Connection` gains `token string` and `validate func(string) (string, error)`
  (a small func field keeps the actor testable without a real validator).
- `NewConnection(conn, userID, token string, v *server... , registry)` —
  signature note: take the validate func as `func(string) (string, error)`
  to avoid an import cycle (`ws` must not import `server`); the handler
  passes `s.auth.Validate`.
- In `handleFrame`:

```go
case TypePing:
	if f.Token != "" {
		// Refresh the lease. Same-subject check: a connection's identity
		// is fixed at upgrade; a token for someone else is an error.
		sub, err := cn.validate(f.Token)
		if err != nil || sub != cn.userID {
			cn.write(Frame{Type: TypeError, Reason: "token refresh rejected"})
		} else {
			cn.token = f.Token
		}
	}
	cn.write(Frame{Type: TypePong})
```

- In `TypeSend`, include the token on the actor message:

```go
c.Send(pid, room.Send{
	UserID:   cn.userID,
	Body:     f.Body,
	ClientID: f.ClientID,
	Token:    cn.token,
})
```

## 8. `internal/room/messages.go`

`Send` gains `Token string` (doc comment: bearer JWT of the sender; the
Convex mutation derives identity from it, PRD §13).

## 9. `internal/convex/client.go` — per-request auth

```go
// WithAuth returns a shallow copy of the client that authenticates as the
// bearer of token. Cheap: copies share the underlying http.Client.
func (c *Client) WithAuth(token string) *Client {
	clone := *c
	clone.authToken = token
	return &clone
}
```

## 10. `internal/room/store.go`

- Interface: `SendMessage(ctx context.Context, roomID, body, clientID, token string) (Message, error)`
  — **`userID` is gone** (the mutation rejects unknown args and derives the
  user from the JWT; this was one of the three breaks in the dead send path).
- Implementation:

```go
func (s *ConvexStore) SendMessage(ctx context.Context, roomID, body, clientID, token string) (Message, error) {
	var msg Message
	args := map[string]any{
		"roomId":   roomID,
		"body":     body,
		"clientId": clientID,
	}
	if err := s.client.WithAuth(token).Mutation(ctx, "messages:send", args, &msg); err != nil {
		return Message{}, fmt.Errorf("messages:send: %w", err)
	}
	return msg, nil
}
```

## 11. `internal/room/room.go`

`handleSend` passes `msg.Token` through. The idempotency fast-path (seen
cache) is unchanged — a cached ack needs no Convex call, hence no token.

## 12. Web client: `web/src/lib/useChatSocket.ts` + `protocol.ts`

- `protocol.ts`: add `token?: string` to `Frame`.
- In the hook's ping interval, fetch a fresh token (the hook already holds
  `getToken`) and attach it:

```ts
pingTimer = setInterval(() => {
  if (ws.readyState !== WebSocket.OPEN) return;
  void getToken().then((token) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: "ping", ...(token ? { token } : {}) }));
    }
  });
}, PING_INTERVAL_MS);
```

Clerk caches session tokens client-side and refreshes them automatically,
so `getToken()` here is cheap (no network round trip most calls).

## 13. Tests to update / add

- `internal/room`: stub `Store` signature gains `token` — assert the token
  the actor received is the one passed to the store.
- `internal/server/ws_test.go`: the old `?user=` tests die. New harness:
  1. Generate an RSA keypair in the test.
  2. Serve a JWKS (`httptest.Server`) built from the public key.
  3. Point `NewTokenValidator` at it; mint tokens with `golang-jwt`
     (`iss` = test issuer, `aud` = "convex", short `exp`).
  4. Cases: no token → 401; garbage token → 401; expired token → 401;
     valid token → ping/pong works; send → ack (with stubbed store);
     ping with a *different subject's* token → error frame, original
     identity retained.
- `internal/convex`: `WithAuth` clone sends the right `Authorization`
  header and doesn't mutate the original client.

## 14. Definition of done

- [ ] `go build ./... && go vet ./... && go test ./...` green.
- [ ] `bun run build` green in `web/`.
- [ ] `make run` + `bun dev`: socket dot turns **green** after sign-in.
- [ ] Type a message → it renders, ack settles the optimistic bubble, and
      the row appears in Convex `messages` with your Convex `users._id`.
- [ ] Two browsers (or one + incognito), same room → messages flow both
      ways in real time (this is the PRD M2 demo, finally unlocked).
- [ ] Leave a room idle >5 min, send again → still works (room actor
      respawned; Convex dedupe is authoritative — PRD §10.5).
- [ ] Stay connected >2 min and send → still works (proves token refresh;
      without it the token would have expired).
- [ ] `curl -i 'http://localhost:8080/ws'` → 401; with `?token=garbage` → 401.

Ping me when it's in and I'll review the diff.
