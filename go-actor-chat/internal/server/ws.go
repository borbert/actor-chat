package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"

	"github.com/borbert/actor-chat/go-actor-chat/internal/ws"
)

// wsHandler upgrades to a WebSocket and runs the connection's read loop until
// the peer disconnects (PRD §10.3). Identity comes from the "user" query
// parameter as a dev-mode stand-in until M3 replaces it with a WorkOS JWT
// (browsers cannot set headers on WebSocket requests).
func (s *Server) wsHandler(c echo.Context) error {
	if s.rooms == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "CONVEX_URL not configured")
	}

	userID := c.QueryParam("user")
	if userID == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing user identity")
	}

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		OriginPatterns: allowedOrigins(),
	})
	if err != nil {
		// Accept already wrote the HTTP error response.
		return nil
	}

	pid := s.engine.Spawn(ws.NewConnection(conn, userID, s.rooms), "conn")
	ws.ReadLoop(c.Request().Context(), conn, s.engine, pid)

	// Read loop ended: peer closed or timed out. Stop the actor (it closes
	// the socket on Stopped).
	<-s.engine.Poison(pid).Done()
	return nil
}

// allowedOrigins returns WS origin patterns. Defaults cover local dev
// (Vite on 5173); production sets ALLOWED_ORIGINS to its web origin(s).
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
