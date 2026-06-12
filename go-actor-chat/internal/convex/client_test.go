package convex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestQuerySuccess(t *testing.T) {
	client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/query" {
			t.Errorf("path = %q, want /api/query", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["path"] != "health:ping" {
			t.Errorf("function path = %v, want health:ping", body["path"])
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"value":  map[string]any{"now": 42.0},
		})
	})

	var out struct {
		Now float64 `json:"now"`
	}
	if err := client.Query(context.Background(), "health:ping", nil, &out); err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.Now != 42 {
		t.Errorf("now = %v, want 42", out.Now)
	}
}

func TestMutationErrorPaths(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantErrPart string
	}{
		{
			name:        "http error with message",
			status:      http.StatusBadRequest,
			body:        `{"status":"error","errorMessage":"bad args"}`,
			wantErrPart: "bad args",
		},
		{
			name:        "http error without message",
			status:      http.StatusInternalServerError,
			body:        `{}`,
			wantErrPart: "failed with status",
		},
		{
			name:        "convex-level error with 200",
			status:      http.StatusOK,
			body:        `{"status":"error","errorMessage":"Uncaught Error: nope"}`,
			wantErrPart: "Uncaught Error: nope",
		},
		{
			name:        "malformed response body",
			status:      http.StatusOK,
			body:        `not json`,
			wantErrPart: "decode convex",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})

			err := client.Mutation(context.Background(), "messages:send", map[string]any{}, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrPart)
			}
		})
	}
}

func TestAuthHeaders(t *testing.T) {
	var gotAuth, gotDeploy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotDeploy = r.Header.Get("Convex-Deploy-Key")
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "value": nil})
	}))
	defer srv.Close()

	client := New(srv.URL, WithAuthToken("tok123"), WithDeployKey("deploy456"))
	if err := client.Query(context.Background(), "health:ping", nil, nil); err != nil {
		t.Fatalf("query: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization = %q, want Bearer tok123", gotAuth)
	}
	if gotDeploy != "deploy456" {
		t.Errorf("Convex-Deploy-Key = %q, want deploy456", gotDeploy)
	}
}
