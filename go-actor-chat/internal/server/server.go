package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/borbert/go-actor-chat/internal/convex"

)

// Server holds the Echo instance and shared dependencies. Handlers are methods
// on Server so they can reach dependencies without package-level globals.
type Server struct {
	port   int
	echo   *echo.Echo
	convex *convex.Client // nil until CONVEX_URL is configured
}

// New builds the HTTP server, wiring middleware, dependencies, and routes.
func New() *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))
	if port == 0 {
		port = 8080
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())

	s := &Server{port: port, echo: e}

	// The readiness probe calls a public Convex function, so no auth is needed
	// here yet. Server-only writes (e.g. user provisioning in M3) will add a
	// second client built with convex.WithDeployKey(...).
	if url := os.Getenv("CONVEX_URL"); url != "" {
		s.convex = convex.New(url, convex.WithTimeout(5*time.Second))
	}

	s.RegisterRoutes()

	return &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.echo,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}
