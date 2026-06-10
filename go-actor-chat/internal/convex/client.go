package convex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

// Client is a minimal Convex HTTP API client for server-side calls.
type Client struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
	deployKey  string
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the timeout used by the underlying HTTP client.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithAuthToken configures bearer token auth for user-scoped calls.
func WithAuthToken(token string) Option {
	return func(c *Client) {
		c.authToken = token
	}
}

// WithDeployKey configures deploy-key auth for server-side administrative calls.
func WithDeployKey(key string) Option {
	return func(c *Client) {
		c.deployKey = key
	}
}

// New creates a Convex HTTP client for the deployment URL.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Query calls a public Convex query function.
func (c *Client) Query(ctx context.Context, path string, args any, out any) error {
	return c.call(ctx, "query", path, args, out)
}

// Mutation calls a public Convex mutation function.
func (c *Client) Mutation(ctx context.Context, path string, args any, out any) error {
	return c.call(ctx, "mutation", path, args, out)
}

type requestBody struct {
	Path   string `json:"path"`
	Args   any    `json:"args"`
	Format string `json:"format"`
}

type responseBody struct {
	Status       string          `json:"status"`
	Value        json.RawMessage `json:"value"`
	ErrorMessage string          `json:"errorMessage"`
}

func (c *Client) call(ctx context.Context, kind string, path string, args any, out any) error {
	if args == nil {
		args = map[string]any{}
	}

	body, err := json.Marshal(requestBody{
		Path:   path,
		Args:   args,
		Format: "json",
	})
	if err != nil {
		return fmt.Errorf("marshal convex %s request: %w", kind, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/"+kind, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build convex %s request: %w", kind, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	if c.deployKey != "" {
		req.Header.Set("Convex-Deploy-Key", c.deployKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call convex %s %q: %w", kind, path, err)
	}
	defer resp.Body.Close()

	var decoded responseBody
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode convex %s response: %w", kind, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if decoded.ErrorMessage != "" {
			return fmt.Errorf("convex %s %q failed: %s", kind, path, decoded.ErrorMessage)
		}
		return fmt.Errorf("convex %s %q failed with status %s", kind, path, resp.Status)
	}
	if decoded.Status != "success" {
		if decoded.ErrorMessage != "" {
			return fmt.Errorf("convex %s %q failed: %s", kind, path, decoded.ErrorMessage)
		}
		return fmt.Errorf("convex %s %q failed with status %q", kind, path, decoded.Status)
	}
	if out == nil || len(decoded.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(decoded.Value, out); err != nil {
		return fmt.Errorf("decode convex %s value: %w", kind, err)
	}
	return nil
}
