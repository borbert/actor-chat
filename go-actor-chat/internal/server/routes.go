package server

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/borbert/actor-chat/go-actor-chat/internal/ping"
)

// RegisterRoutes wires all HTTP routes. Kept separate from server lifecycle so
// the route table is easy to scan in one place.
func (s *Server) RegisterRoutes() {
	s.echo.GET("/health", s.healthHandler)
	s.echo.GET("/ready", s.readyHandler)
	s.echo.GET("/ws", s.wsHandler)
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


	resp := s.engine.Request(s.pingPID, ping.Ping{Nonce: "ready"}, time.Second)
 	res, err := resp.Result() // blocks up to the timeout
  	if err != nil {
        return c.JSON(http.StatusServiceUnavailable, map[string]string{
                "status": "not ready",
                "reason": "actor engine unreachable: " + err.Error(),
        })
 	 }
  	if _, ok := res.(ping.Pong); !ok {
        // defensive: wrong reply type
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
                "status": "not ready",
                "reason": "actor engine returned unexpected reply",
        })

  	}

	return c.JSON(http.StatusOK, map[string]any{
		"status":     "ready",
		"convex":     "reachable",
		"convex_now": out.Now,
		"actor":      "reachable",
	})
}

