package onlinelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBootstrapAdminClaimsLegacyCards(t *testing.T) {
	store, service := newRBACService(t)
	now := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	card := &CardRecord{
		CardHash:   HashCardKey("legacy-card"),
		Plan:       PlanMonthly,
		Days:       30,
		MaxDevices: 1,
		Status:     CardStatusActive,
		CreatedAt:  now,
	}
	if err := store.CreateCard(card); err != nil {
		t.Fatalf("create legacy card: %v", err)
	}

	root, err := service.EnsureBootstrapAdmin("admin", "secret")
	if err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	cards, err := service.ListCardsFor(root)
	if err != nil {
		t.Fatalf("list cards for root: %v", err)
	}
	if len(cards) != 1 || cards[0].ID != card.ID || cards[0].OwnerAccountID != root.ID {
		t.Fatalf("legacy card was not claimed by root: %#v root=%#v", cards, root)
	}
}

func TestResellerAndDistributorScopesAreEnforced(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)

	reseller := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username:    "reseller",
		Password:    "reseller-pass",
		DisplayName: "Reseller",
		Level:       AdminLevelReseller,
	})
	distributor := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username:    "distributor",
		Password:    "distributor-pass",
		DisplayName: "Distributor",
		Level:       AdminLevelDistributor,
		ParentID:    reseller.ID,
	})
	other := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username:    "other",
		Password:    "other-pass",
		DisplayName: "Other",
		Level:       AdminLevelReseller,
	})

	rootOwnedKey := generateHTTPCardForOwner(t, handler, rootCookie, other.ID, 1)
	resellerCookie := loginAdminAs(t, handler, "reseller", "reseller-pass")
	resellerOwnedKey := generateHTTPCardForOwner(t, handler, resellerCookie, distributor.ID, 1)

	activateHTTPCard(t, handler, rootOwnedKey, "device-other")
	activateHTTPCard(t, handler, resellerOwnedKey, "device-distributor")

	resellerCards := listAdminCards(t, handler, resellerCookie)
	if len(resellerCards) != 1 || resellerCards[0].OwnerAccountID != distributor.ID {
		t.Fatalf("reseller cards = %#v, want only distributor-owned card", resellerCards)
	}

	distributorCookie := loginAdminAs(t, handler, "distributor", "distributor-pass")
	distributorDevices := listAdminDevices(t, handler, distributorCookie)
	if len(distributorDevices) != 1 || distributorDevices[0].DeviceID != "device-distributor" {
		t.Fatalf("distributor devices = %#v, want own activated device only", distributorDevices)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/activations/"+strconv.FormatInt(distributorDevices[0].CurrentActivationID, 10)+"/disable", nil)
	req.AddCookie(loginAdminAs(t, handler, "other", "other-pass"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sibling disable status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
}

func TestDefaultLowerLevelAccountsCannotDeleteRecords(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	reseller := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	resellerCookie := loginAdminAs(t, handler, "reseller", "reseller-pass")
	generateHTTPCardForOwner(t, handler, rootCookie, reseller.ID, 1)
	cards := listAdminCards(t, handler, resellerCookie)
	if len(cards) != 1 {
		t.Fatalf("reseller cards = %d, want 1", len(cards))
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/cards/"+strconv.FormatInt(cards[0].ID, 10), nil)
	req.AddCookie(resellerCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reseller delete status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
}

func TestDisabledAdminAccountCannotContinueUsingSession(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	account := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	resellerCookie := loginAdminAs(t, handler, "reseller", "reseller-pass")

	req := httptest.NewRequest(http.MethodPatch, "/api/admin/accounts/"+strconv.FormatInt(account.ID, 10), strings.NewReader(`{"status":"disabled"}`))
	req.AddCookie(rootCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable account status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/cards", nil)
	req.AddCookie(resellerCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled account session status = %d body=%s, want 401", rec.Code, rec.Body.String())
	}
}

func TestAdminAccountCannotChangeOwnPrivileges(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)

	self := currentAdminAccount(t, handler, rootCookie)
	for name, body := range map[string]string{
		"permissions":       `{"permissions":["cards:view"]}`,
		"empty_permissions": `{"permissions":[]}`,
		"status":            `{"status":"disabled"}`,
		"level":             `{"level":2}`,
		"parent":            `{"parentId":99}`,
		"root_parent":       `{"parentId":0}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api/admin/accounts/"+strconv.FormatInt(self.ID, 10), strings.NewReader(body))
		req.AddCookie(rootCookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s self update status = %d body=%s, want 403", name, rec.Code, rec.Body.String())
		}
	}
}

func TestDevicesIncludeOwnerInformation(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	account := currentAdminAccount(t, handler, rootCookie)
	cardKey := generateHTTPCardForOwner(t, handler, rootCookie, account.ID, 1)
	activateHTTPCard(t, handler, cardKey, "device-owner")

	devices := listAdminDevices(t, handler, rootCookie)
	if len(devices) != 1 || devices[0].OwnerAccountID != account.ID || devices[0].OwnerUsername != account.Username {
		t.Fatalf("device owner = %#v, want account %#v", devices, account)
	}
}

func TestNonRootAdminResponsesHideAbsoluteLevels(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	reseller := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	distributor := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "distributor",
		Password: "distributor-pass",
		Level:    AdminLevelDistributor,
		ParentID: reseller.ID,
	})

	resellerCookie := loginAdminAs(t, handler, "reseller", "reseller-pass")
	me := currentAdminAccount(t, handler, resellerCookie)
	if me.Level != 0 || me.Relationship != AdminRelationshipSelf {
		t.Fatalf("non-root /me account = %#v, want hidden level and self relationship", me)
	}

	accounts := listAdminAccounts(t, handler, resellerCookie)
	if len(accounts) != 2 {
		t.Fatalf("reseller accounts = %#v, want self and downline", accounts)
	}
	for _, account := range accounts {
		if account.Level != 0 {
			t.Fatalf("non-root account %s exposed absolute level %d", account.Username, account.Level)
		}
		switch account.ID {
		case reseller.ID:
			if account.Relationship != AdminRelationshipSelf {
				t.Fatalf("reseller relationship = %q, want self", account.Relationship)
			}
		case distributor.ID:
			if account.Relationship != AdminRelationshipDownline {
				t.Fatalf("distributor relationship = %q, want downline", account.Relationship)
			}
		default:
			t.Fatalf("unexpected account in reseller scope: %#v", account)
		}
	}
}

func TestRootAdminResponsesKeepAbsoluteLevels(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})

	root := currentAdminAccount(t, handler, rootCookie)
	if root.Level != AdminLevelRoot {
		t.Fatalf("root /me level = %d, want %d", root.Level, AdminLevelRoot)
	}
	accounts := listAdminAccounts(t, handler, rootCookie)
	levels := map[string]int{}
	for _, account := range accounts {
		levels[account.Username] = account.Level
	}
	if levels["admin"] != AdminLevelRoot || levels["reseller"] != AdminLevelReseller {
		t.Fatalf("root account levels = %#v, want absolute levels", levels)
	}
}

func TestNonRootCannotEscalateAccountHierarchyToRoot(t *testing.T) {
	_, service := newRBACService(t)
	root, err := service.EnsureBootstrapAdmin("root", "root-pass")
	if err != nil {
		t.Fatalf("bootstrap root: %v", err)
	}
	reseller, err := service.CreateAdminAccount(root, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	if err != nil {
		t.Fatalf("create reseller: %v", err)
	}

	if _, err := service.CreateAdminAccount(reseller, CreateAdminAccountRequest{
		Username: "forged-root",
		Password: "forged-pass",
		Level:    AdminLevelRoot,
		ParentID: reseller.ID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reseller create-root error = %v, want ErrForbidden", err)
	}
	if _, err := service.CreateAdminAccount(reseller, CreateAdminAccountRequest{
		Username: "peer-reseller",
		Password: "peer-pass",
		Level:    AdminLevelReseller,
		ParentID: reseller.ID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reseller create-peer error = %v, want ErrForbidden", err)
	}

	distributor, err := service.CreateAdminAccount(reseller, CreateAdminAccountRequest{
		Username: "distributor",
		Password: "distributor-pass",
		Level:    AdminLevelDistributor,
		ParentID: reseller.ID,
	})
	if err != nil {
		t.Fatalf("create distributor: %v", err)
	}
	if _, err := service.UpdateAdminAccount(reseller, distributor.ID, UpdateAdminAccountRequest{
		Level:    AdminLevelRoot,
		hasLevel: true,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reseller promote-to-root error = %v, want ErrForbidden", err)
	}
}

func newRBACService(t *testing.T) (*SQLiteStore, *Service) {
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
		return time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	}})
	return store, service
}

func createAdminAccount(t *testing.T, handler http.Handler, cookie *http.Cookie, request CreateAdminAccountRequest) AdminAccount {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal account request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts", strings.NewReader(string(body)))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create account status = %d body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data AdminAccount `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	return decoded.Data
}

func currentAdminAccount(t *testing.T, handler http.Handler, cookie *http.Cookie) AdminAccount {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("current admin status = %d body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data struct {
			Account AdminAccount `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode current admin: %v", err)
	}
	return decoded.Data.Account
}

func listAdminAccounts(t *testing.T, handler http.Handler, cookie *http.Cookie) []AdminAccount {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list accounts status = %d body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data []AdminAccount `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}
	return decoded.Data
}

func loginAdminAs(t *testing.T, handler http.Handler, username, password string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s status = %d body=%s", username, rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == adminSessionCookieName && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("login response did not set %s cookie", adminSessionCookieName)
	return nil
}

func generateHTTPCardForOwner(t *testing.T, handler http.Handler, cookie *http.Cookie, ownerID int64, maxDevices int) string {
	t.Helper()
	body := `{"plan":"monthly","count":1,"maxDevices":` + strconv.Itoa(maxDevices) + `,"ownerAccountId":` + strconv.FormatInt(ownerID, 10) + `}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/cards/generate", strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate for owner %d status = %d body=%s", ownerID, rec.Code, rec.Body.String())
	}
	var generated struct {
		Data struct {
			Cards []GeneratedCard `json:"cards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &generated); err != nil {
		t.Fatalf("decode generate: %v", err)
	}
	return generated.Data.Cards[0].CardKey
}

func activateHTTPCard(t *testing.T, handler http.Handler, cardKey, deviceID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(`{"cardKey":"`+cardKey+`","deviceId":"`+deviceID+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate %s status = %d body=%s", deviceID, rec.Code, rec.Body.String())
	}
}

func listAdminDevices(t *testing.T, handler http.Handler, cookie *http.Cookie) []DeviceRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/devices", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices status = %d body=%s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data []DeviceRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	return decoded.Data
}

var _ = sql.ErrNoRows
