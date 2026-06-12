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
