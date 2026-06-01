package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackOrigin(t *testing.T) {
	cases := map[string]bool{
		"":                         false,
		"http://localhost:3000":    true,
		"http://127.0.0.1:3000":    true,
		"http://[::1]:3000":        true,
		"https://evil.example.com": false,
		"http://10.0.0.5":          false,
		"not a url":                false,
	}
	for origin, want := range cases {
		if got := isLoopbackOrigin(origin); got != want {
			t.Errorf("isLoopbackOrigin(%q)=%v want %v", origin, got, want)
		}
	}
}

func TestCORSMiddlewareReflectsOnlyLoopback(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORSMiddleware(next)

	// Loopback origin is reflected.
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("loopback origin not reflected: %q", got)
	}

	// Remote origin is NOT reflected and not wildcarded.
	req2 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req2.Header.Set("Origin", "https://evil.example.com")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("remote origin should not be allowed, got %q", got)
	}
}
