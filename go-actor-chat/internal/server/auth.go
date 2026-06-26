package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/borbert/actor-chat/go-actor-chat/internal/convex"
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

// ProvisionUser exchanges a validated token for the caller's Convex
// users._id, creating the row on first contact. Idempotent — the web client
// calls the same mutation at sign-in (PRD §11, §13). Returned as a func, not
// a method, so the WS handler can hold it as a field the tests can stub
// without a real Convex deployment.
func ProvisionUser(client *convex.Client) func(ctx context.Context, token string) (string, error) {
	return func(ctx context.Context, token string) (string, error) {
		var user struct {
			ID string `json:"_id"`
		}
		if err := client.WithAuth(token).Mutation(ctx, "users:getOrCreateFromAuth", map[string]any{}, &user); err != nil {
			return "", fmt.Errorf("users:getOrCreateFromAuth: %w", err)
		}
		if user.ID == "" {
			return "", fmt.Errorf("users:getOrCreateFromAuth returned no _id")
		}
		return user.ID, nil
	}
}
