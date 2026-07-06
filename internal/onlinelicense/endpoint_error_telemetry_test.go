package onlinelicense

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointErrorTelemetrySubmitAggregatesAndRejectsWrongDevice(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cardKey := generateHTTPCard(t, handler, 1)
	activated := activateHTTPCardWithResult(t, handler, cardKey, "device-a")

	body := endpointTelemetryBody(activated.Data.Ticket, "device-a", "Primary", "rate_limited", 429, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d body=%s", rec.Code, rec.Body.String())
	}
	var submitted struct {
		Success bool                         `json:"success"`
		Data    EndpointErrorTelemetryResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitted.Data.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", submitted.Data.Accepted)
	}

	body = endpointTelemetryBody(activated.Data.Ticket, "device-a", "Primary", "rate_limited", 429, 2)
	req = httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry submit status = %d body=%s", rec.Code, rec.Body.String())
	}

	body = endpointTelemetryBody(activated.Data.Ticket, "device-a", "Primary", "rate_limited", 429, 5)
	req = httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("higher cumulative submit status = %d body=%s", rec.Code, rec.Body.String())
	}

	cookie := loginAdmin(t, handler)
	req = httptest.NewRequest(http.MethodGet, "/api/admin/telemetry/endpoint-errors?deviceId=device-a", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Success bool                           `json:"success"`
		Data    EndpointErrorTelemetryResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	if len(listed.Data.Items) != 1 || listed.Data.Items[0].Count != 5 {
		t.Fatalf("listed telemetry = %#v, want one idempotent cumulative count 5", listed.Data.Items)
	}
	if strings.Contains(rec.Body.String(), "sk-secret") || strings.Contains(rec.Body.String(), "prompt text") || strings.Contains(rec.Body.String(), "response text") {
		t.Fatalf("telemetry response leaked sensitive data: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "api_key=secret") || strings.Contains(rec.Body.String(), "Bearer secret-token") || strings.Contains(rec.Body.String(), "?api_key") {
		t.Fatalf("telemetry response leaked query or auth sample data: %s", rec.Body.String())
	}

	wrongDeviceBody := endpointTelemetryBody(activated.Data.Ticket, "device-b", "Primary", "rate_limited", 429, 1)
	req = httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(wrongDeviceBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong device status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestEndpointErrorTelemetryRetentionCleanup(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	oldWindow := now.AddDate(0, 0, -91)
	recentWindow := now.Add(-time.Hour)

	if _, err := store.RecordEndpointErrorTelemetry("device-a", 1, 1, "darwin", "6.7.3", []EndpointErrorTelemetryItem{{
		EndpointName:        "Old",
		EndpointFingerprint: "old-fp",
		Reason:              "upstream_5xx",
		StatusCode:          500,
		Count:               1,
		WindowStart:         oldWindow,
		WindowEnd:           oldWindow.Add(5 * time.Minute),
		FirstAt:             oldWindow,
		LastAt:              oldWindow,
	}}, oldWindow); err != nil {
		t.Fatalf("record old telemetry: %v", err)
	}
	if _, err := store.RecordEndpointErrorTelemetry("device-a", 1, 1, "darwin", "6.7.3", []EndpointErrorTelemetryItem{{
		EndpointName:        "Recent",
		EndpointFingerprint: "recent-fp",
		Reason:              "rate_limited",
		StatusCode:          429,
		Count:               2,
		WindowStart:         recentWindow,
		WindowEnd:           recentWindow.Add(5 * time.Minute),
		FirstAt:             recentWindow,
		LastAt:              recentWindow,
	}}, now); err != nil {
		t.Fatalf("record recent telemetry: %v", err)
	}

	items, err := store.ListEndpointErrorTelemetry(EndpointErrorTelemetryQuery{DeviceID: "device-a", From: now.AddDate(0, 0, -120)})
	if err != nil {
		t.Fatalf("list telemetry after retention cleanup: %v", err)
	}
	if len(items) != 1 || items[0].EndpointName != "Recent" {
		t.Fatalf("retained telemetry = %#v, want only recent row", items)
	}
}

func TestEndpointErrorTelemetryAdminRequiresSessionAndScope(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	reseller := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "telemetry-reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	other := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "telemetry-other",
		Password: "other-pass",
		Level:    AdminLevelReseller,
	})

	resellerKey := generateHTTPCardForOwner(t, handler, rootCookie, reseller.ID, 1)
	otherKey := generateHTTPCardForOwner(t, handler, rootCookie, other.ID, 1)
	resellerActivation := activateHTTPCardWithResult(t, handler, resellerKey, "device-reseller")
	otherActivation := activateHTTPCardWithResult(t, handler, otherKey, "device-other")
	submitEndpointTelemetry(t, handler, resellerActivation.Data.Ticket, "device-reseller")
	submitEndpointTelemetry(t, handler, otherActivation.Data.Ticket, "device-other")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/telemetry/endpoint-errors?deviceId=device-reseller", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", rec.Code)
	}

	resellerCookie := loginAdminAs(t, handler, "telemetry-reseller", "reseller-pass")
	req = httptest.NewRequest(http.MethodGet, "/api/admin/telemetry/endpoint-errors?deviceId=device-reseller", nil)
	req.AddCookie(resellerCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("own telemetry status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/telemetry/endpoint-errors?deviceId=device-other", nil)
	req.AddCookie(resellerCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sibling telemetry status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
}

func TestEndpointErrorTelemetryAdminFiltersStatusCodeZero(t *testing.T) {
	handler := newTestHTTPHandler(t)
	cardKey := generateHTTPCard(t, handler, 1)
	activated := activateHTTPCardWithResult(t, handler, cardKey, "device-a")
	submitEndpointTelemetry(t, handler, activated.Data.Ticket, "device-a")

	body := endpointTelemetryBody(activated.Data.Ticket, "device-a", "Primary", "transient_network_error", 0, 4)
	req := httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit zero status telemetry status = %d body=%s", rec.Code, rec.Body.String())
	}

	cookie := loginAdmin(t, handler)
	req = httptest.NewRequest(http.MethodGet, "/api/admin/telemetry/endpoint-errors?deviceId=device-a&statusCode=0", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin zero status filter = %d body=%s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Success bool                           `json:"success"`
		Data    EndpointErrorTelemetryResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode zero status list: %v", err)
	}
	if len(listed.Data.Items) != 1 || listed.Data.Items[0].StatusCode != 0 || listed.Data.Items[0].Reason != "transient_network_error" {
		t.Fatalf("zero status filtered telemetry = %#v", listed.Data.Items)
	}
}

func endpointTelemetryBody(ticket, deviceID, endpointName, reason string, statusCode, count int) string {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	req := EndpointErrorTelemetryRequest{
		Ticket:     ticket,
		DeviceID:   deviceID,
		Platform:   "darwin",
		AppVersion: "6.7.3",
		Items: []EndpointErrorTelemetryItem{{
			EndpointName:        endpointName,
			EndpointFingerprint: "ep-fp",
			APIHost:             "api.example.test",
			APIURLFingerprint:   "url-fp",
			AuthMode:            "api_key",
			Transformer:         "openai2",
			Model:               "gpt-5",
			Reason:              reason,
			StatusCode:          statusCode,
			Count:               count,
			WindowStart:         now,
			WindowEnd:           now.Add(5 * time.Minute),
			FirstAt:             now.Add(time.Minute),
			LastAt:              now.Add(2 * time.Minute),
			Sample:              "prompt text response text sk-secret Bearer secret-token https://api.example.test/v1?api_key=secret",
		}},
	}
	data, _ := json.Marshal(req)
	return string(data)
}

func submitEndpointTelemetry(t *testing.T, handler http.Handler, ticket, deviceID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/license/telemetry/endpoint-errors", strings.NewReader(endpointTelemetryBody(ticket, deviceID, "Primary", "upstream_5xx", 500, 1)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit telemetry status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func activateHTTPCardWithResult(t *testing.T, handler http.Handler, cardKey, deviceID string) struct {
	Success bool             `json:"success"`
	Data    ActivationResult `json:"data"`
} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(`{"cardKey":"`+cardKey+`","deviceId":"`+deviceID+`","platform":"darwin","appVersion":"6.7.3"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate %s status = %d body=%s", deviceID, rec.Code, rec.Body.String())
	}
	var decoded struct {
		Success bool             `json:"success"`
		Data    ActivationResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode activation: %v", err)
	}
	return decoded
}
