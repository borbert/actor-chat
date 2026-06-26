package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/anthdm/hollywood/actor"
	"github.com/borbert/actor-chat/go-actor-chat/internal/convex"
	"github.com/borbert/actor-chat/go-actor-chat/internal/ping"
	"github.com/borbert/actor-chat/go-actor-chat/internal/room"
)

// Server holds the Echo instance and shared dependencies. Handlers are methods
// on Server so they can reach dependencies without package-level globals.
type Server struct {
	port    int
	echo    *echo.Echo
	convex  *convex.Client // nil until CONVEX_URL is configured
	engine  *actor.Engine
	pingPID *actor.PID
	rooms   *room.Registry  // nil until CONVEX_URL is configured
	auth    *TokenValidator // nil until CLERK_JWT_ISSUER_DOMAIN is configured

	// resolveUser exchanges a validated token for the caller's Convex
	// users._id (nil until CONVEX_URL is configured). A field so tests can
	// stub it without Convex.
	resolveUser func(ctx context.Context, token string) (string, error)
}

// New builds the HTTP server, wiring middleware, dependencies, and routes.
func New() (*http.Server, error) {

	_ = godotenv.Load(".env.local")
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

	eng, err := actor.NewEngine(actor.NewEngineConfig())
  	if err != nil {
        return nil, fmt.Errorf("create actor engine: %w", err)
  	}

	// The readiness probe calls a public Convex function, so no auth is needed
	// here yet. Server-only writes (e.g. user provisioning in M3) will add a
	// second client built with convex.WithDeployKey(...).
	if url := os.Getenv("CONVEX_URL"); url != "" {
		s.convex = convex.New(url, convex.WithTimeout(5*time.Second))
		s.rooms = room.NewRegistry(eng, room.NewConvexStore(s.convex))
		s.resolveUser = ProvisionUser(s.convex)
	}

	// JWT validation for the WS upgrade (PRD §13). The JWKS cache lives as
	// long as the process, hence context.Background().
	if issuer := os.Getenv("CLERK_JWT_ISSUER_DOMAIN"); issuer != "" {
		validator, err := NewTokenValidator(context.Background(), issuer)
		if err != nil {
			return nil, fmt.Errorf("clerk token validator: %w", err)
		}
		s.auth = validator
	}

	s.engine = eng
  	s.pingPID = eng.Spawn(ping.New, "ping") // ping.New is the producer; "ping" is the actor id


	s.RegisterRoutes()

	return &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.echo,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}, nil
}
