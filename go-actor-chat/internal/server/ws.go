package server

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"

	"github.com/borbert/actor-chat/go-actor-chat/internal/ws"
)

// wsHandler upgrades to a WebSocket and runs the connection's read loop until
// the peer disconnects (PRD §10.3). Identity comes from a Clerk JWT in the
// "token" query parameter (browsers cannot set headers on WebSocket
// requests), validated against the instance JWKS (PRD §13).
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
		// Detail stays server-side; the client only learns "invalid".
		slog.Warn("ws token rejected", "err", err)
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		OriginPatterns: allowedOrigins(),
	})
	if err != nil {
		// Accept already wrote the HTTP error response.
		return nil
	}

	pid := s.engine.Spawn(ws.NewConnection(conn, userID, token, s.auth.Validate, s.rooms), "conn")
	ws.ReadLoop(c.Request().Context(), conn, s.engine, pid)

	// Read loop ended: peer closed or timed out. Stop the actor (it closes
	// the socket on Stopped).
	<-s.engine.Poison(pid).Done()
	return nil
}

// allowedOrigins returns WS origin patterns. Defaults cover local dev
// (Vite on 5174); production sets ALLOWED_ORIGINS to its web origin(s).
func allowedOrigins() []string {
	if env := os.Getenv("ALLOWED_ORIGINS"); env != "" {
		parts := strings.Split(env, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return []string{"localhost:*", "127.0.0.1:*"}
}
