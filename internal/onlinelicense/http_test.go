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

func TestAdminEndpointsRequireSession(t *testing.T) {
	handler := newTestHTTPHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("basic auth status = %d, want 401", rec.Code)
	}
}

func TestAdminSessionLoginAndLogout(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cookie := loginAdmin(t, handler)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized cards status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout cards status = %d, want 401", rec.Code)
	}
}

func TestAdminPageMiddlewareProtectsAdminPage(t *testing.T) {
	handler := newTestHTTPHandler(t).(*HTTPHandler)
	page := handler.AdminPageMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("admin page"))
	}))

	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("unauthorized status = %d, want 302", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(loginAdmin(t, handler))
	rec = httptest.NewRecorder()
	page.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "admin page" {
		t.Fatalf("authorized status/body = %d %q", rec.Code, rec.Body.String())
	}
}

func TestAdminCanGenerateCardAndClientCanActivateWithoutAdminAuth(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cookie := loginAdmin(t, handler)

	body := `{"plan":"monthly","count":1,"maxDevices":1,"customer":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cards/generate", strings.NewReader(body))
	req.AddCookie(cookie)
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

func TestAdminLoginRateLimit(t *testing.T) {
	handler := newTestHTTPHandler(t)
	body := `{"username":"admin","password":"wrong"}`

	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
		req.RemoteAddr = "203.0.113.10:41000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if i < 5 && rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401 body=%s", i+1, rec.Code, rec.Body.String())
		}
		if i == 5 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want 429 body=%s", i+1, rec.Code, rec.Body.String())
		}
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

func TestAdminCanDeleteCardAndViewHistory(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cookie := loginAdmin(t, handler)
	cardKey := generateHTTPCard(t, handler, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(`{"cardKey":"`+cardKey+`","deviceId":"device-a"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d body=%s", rec.Code, rec.Body.String())
	}

	cards := listAdminCards(t, handler, cookie)
	if len(cards) != 1 {
		t.Fatalf("cards before delete = %d, want 1", len(cards))
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/admin/cards/"+strconv.FormatInt(cards[0].ID, 10), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}

	cards = listAdminCards(t, handler, cookie)
	if len(cards) != 0 {
		t.Fatalf("cards after delete = %d, want 0", len(cards))
	}

	req = httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(`{"cardKey":"`+cardKey+`","deviceId":"device-b"}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("activation after delete status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/history", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d body=%s", rec.Code, rec.Body.String())
	}
	var history struct {
		Data []AuditRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	actions := map[string]bool{}
	for _, item := range history.Data {
		actions[item.Action] = true
	}
	for _, action := range []string{"admin_login", "generate_card", "activate", "delete_card"} {
		if !actions[action] {
			t.Fatalf("history missing action %q: %#v", action, history.Data)
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

func loginAdmin(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == adminSessionCookieName && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("login response did not set %s cookie", adminSessionCookieName)
	return nil
}

func listAdminCards(t *testing.T, handler http.Handler, cookie *http.Cookie) []CardRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list cards status = %d body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data []CardRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode cards: %v", err)
	}
	return decoded.Data
}

func generateHTTPCard(t *testing.T, handler http.Handler, maxDevices int) string {
	t.Helper()
	cookie := loginAdmin(t, handler)
	body := `{"plan":"monthly","count":1,"maxDevices":` + strconv.Itoa(maxDevices) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cards/generate", strings.NewReader(body))
	req.AddCookie(cookie)
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
