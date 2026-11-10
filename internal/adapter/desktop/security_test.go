// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// serve runs a request through the middleware in front of a handler that
// answers 204 and records the body it received.
func serve(t *testing.T, csp string, req *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("downstream body read: %v", err)
		}
		seen = string(b)
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	securityMiddleware(csp, testLogger)(next).ServeHTTP(rec, req)
	return rec, seen
}

func TestContentSecurityPolicyIsStrict(t *testing.T) {
	csp := strings.TrimSpace(contentSecurityPolicy)
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy lacks %q: %s", want, csp)
		}
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval", "http:", "https:", "*", "data:"} {
		if strings.Contains(csp, bad) {
			t.Errorf("policy must not contain %q: %s", bad, csp)
		}
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	rec, _ := serve(t, contentSecurityPolicy, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != strings.TrimSpace(contentSecurityPolicy) {
		t.Errorf("Content-Security-Policy = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
}

func TestDevModeOmitsCSP(t *testing.T) {
	rec, _ := serve(t, "", httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP must be absent in dev mode, got %q", got)
	}
}

func TestRuntimeObjectAllowlist(t *testing.T) {
	tests := []struct {
		name   string
		object string
		want   int
	}{
		{"call", "0", http.StatusNoContent},
		{"clipboard", "1", http.StatusForbidden},
		{"application", "2", http.StatusForbidden},
		{"events", "3", http.StatusNoContent},
		{"context menu", "4", http.StatusForbidden},
		{"dialog", "5", http.StatusForbidden},
		{"window", "6", http.StatusNoContent},
		{"screens", "7", http.StatusForbidden},
		{"system", "8", http.StatusNoContent},
		{"browser", "9", http.StatusForbidden},
		{"cancel call", "10", http.StatusNoContent},
		{"unknown", "99", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"object":` + tc.object + `,"method":0,"args":{"url":"ms-settings:"}}`
			req := httptest.NewRequest(http.MethodPost, runtimePath, strings.NewReader(body))
			rec, seen := serve(t, contentSecurityPolicy, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusNoContent && seen != body {
				t.Errorf("downstream body = %q, want the original %q", seen, body)
			}
		})
	}
}

func TestRuntimeObjectFromQueryWhenBodyEmpty(t *testing.T) {
	denied, _ := serve(t, contentSecurityPolicy, httptest.NewRequest(http.MethodPost, runtimePath+"?object=9&method=0", nil))
	if denied.Code != http.StatusForbidden {
		t.Errorf("browser via query: status = %d, want 403", denied.Code)
	}
	allowed, _ := serve(t, contentSecurityPolicy, httptest.NewRequest(http.MethodPost, runtimePath+"?object=3&method=0", nil))
	if allowed.Code != http.StatusNoContent {
		t.Errorf("events via query: status = %d, want 204", allowed.Code)
	}
}

func TestRuntimeMalformedCallsAreRejected(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `{`,
		"missing object": `{"method":0}`,
		"empty":          ``,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, runtimePath, strings.NewReader(body))
			rec, _ := serve(t, contentSecurityPolicy, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestChunkedAndOversizedRuntimeCallsAreRefused(t *testing.T) {
	chunked := httptest.NewRequest(http.MethodPost, runtimePath, strings.NewReader(`{"object":3,"method":0}`))
	chunked.Header.Set(chunkIDHeader, "abc")
	if rec, _ := serve(t, contentSecurityPolicy, chunked); rec.Code != http.StatusForbidden {
		t.Errorf("chunked: status = %d, want 403", rec.Code)
	}

	huge := httptest.NewRequest(http.MethodPost, runtimePath, strings.NewReader(`{"object":3,"method":0,"args":"`+strings.Repeat("a", maxRuntimeBody)+`"}`))
	if rec, _ := serve(t, contentSecurityPolicy, huge); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: status = %d, want 413", rec.Code)
	}
}

func TestOtherPathsAreNotInspected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/assets/app.js", strings.NewReader(`{"object":9}`))
	rec, seen := serve(t, contentSecurityPolicy, req)
	if rec.Code != http.StatusNoContent || seen != `{"object":9}` {
		t.Errorf("status = %d, body = %q; non-runtime paths must pass through untouched", rec.Code, seen)
	}
}
