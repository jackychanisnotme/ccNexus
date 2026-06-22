package onlinelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdminEndpointsRequireBasicAuth(t *testing.T) {
	handler := newTestHTTPHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAdminMiddlewareProtectsAdminPage(t *testing.T) {
	page := AdminMiddleware(AdminConfig{Username: "admin", Password: "secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("admin page"))
	}))

	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	page.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "admin page" {
		t.Fatalf("authorized status/body = %d %q", rec.Code, rec.Body.String())
	}
}

func TestAdminCanGenerateCardAndClientCanActivateWithoutAdminAuth(t *testing.T) {
	handler := newTestHTTPHandler(t)

	body := `{"plan":"monthly","count":1,"maxDevices":1,"customer":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cards/generate", strings.NewReader(body))
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var generated struct {
		Success bool `json:"success"`
		Data    struct {
			Cards []GeneratedCard `json:"cards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &generated); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	if len(generated.Data.Cards) != 1 || generated.Data.Cards[0].CardKey == "" {
		t.Fatalf("unexpected generated response: %s", rec.Body.String())
	}

	activateBody := `{"cardKey":"` + generated.Data.Cards[0].CardKey + `","deviceId":"device-a","platform":"darwin","appVersion":"6.0.1"}`
	req = httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(activateBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var activated struct {
		Success bool             `json:"success"`
		Data    ActivationResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &activated); err != nil {
		t.Fatalf("decode activate response: %v", err)
	}
	if !activated.Data.Licensed || activated.Data.Ticket == "" {
		t.Fatalf("activation response not licensed: %s", rec.Body.String())
	}
}

func TestActivationEndpointReturnsBadRequestForDeviceLimit(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cardKey := generateHTTPCard(t, handler, 1)

	for _, deviceID := range []string{"device-a", "device-b"} {
		req := httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(`{"cardKey":"`+cardKey+`","deviceId":"`+deviceID+`"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if deviceID == "device-a" && rec.Code != http.StatusOK {
			t.Fatalf("first activation status = %d body=%s", rec.Code, rec.Body.String())
		}
		if deviceID == "device-b" && rec.Code != http.StatusBadRequest {
			t.Fatalf("second activation status = %d, want 400 body=%s", rec.Code, rec.Body.String())
		}
	}
}

func newTestHTTPHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time {
		return time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	}})
	return NewHTTPHandler(service, AdminConfig{Username: "admin", Password: "secret"})
}

func generateHTTPCard(t *testing.T, handler http.Handler, maxDevices int) string {
	t.Helper()
	body := `{"plan":"monthly","count":1,"maxDevices":` + strconv.Itoa(maxDevices) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cards/generate", strings.NewReader(body))
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var generated struct {
		Data struct {
			Cards []GeneratedCard `json:"cards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &generated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return generated.Data.Cards[0].CardKey
}
