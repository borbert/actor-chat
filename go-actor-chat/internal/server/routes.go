package server

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

)

// RegisterRoutes wires all HTTP routes. Kept separate from server lifecycle so
// the route table is easy to scan in one place.
func (s *Server) RegisterRoutes() {
	s.echo.GET("/health", s.healthHandler)
	s.echo.GET("/ready", s.readyHandler)

	// Future (see PRD §11):
	//   s.echo.GET("/ws", s.wsHandler)   // M2 — WebSocket upgrade
}

// healthHandler is a liveness probe: the process is up and serving.
func (s *Server) healthHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// readyHandler is a readiness probe: dependencies are reachable. It calls the
// public Convex function "health:ping" and reports not-ready if Convex is
// unconfigured or unreachable.
func (s *Server) readyHandler(c echo.Context) error {
	if s.convex == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"reason": "CONVEX_URL not configured",
		})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()

	var out struct {
		Now float64 `json:"now"`
	}
	if err := s.convex.Query(ctx, "health:ping", nil, &out); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"reason": "convex unreachable: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"status":     "ready",
		"convex":     "reachable",
		"convex_now": out.Now,
	})
}
