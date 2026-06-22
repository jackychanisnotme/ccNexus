package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
)

type stubLicenseService struct {
	statusResult   interface{}
	activateResult interface{}
	statusErr      error
	activateErr    error
	activatedKey   string
}

func (s *stubLicenseService) Status(time.Time) (interface{}, error) {
	return s.statusResult, s.statusErr
}

func (s *stubLicenseService) Activate(cardKey string, _ time.Time) (interface{}, error) {
	s.activatedKey = cardKey
	return s.activateResult, s.activateErr
}

func TestLicenseAPIRequiresConfiguredService(t *testing.T) {
	handler := NewHandler(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/license/status", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestLicenseAPIStatusReturnsServicePayload(t *testing.T) {
	service := &stubLicenseService{
		statusResult: map[string]any{
			"licensed":      true,
			"remainingDays": float64(29),
		},
	}
	handler := NewHandler(testConfig(), nil, nil, service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/license/status", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected object data, got %#v", resp.Data)
	}
	if data["licensed"] != true {
		t.Fatalf("expected licensed=true, got %#v", data["licensed"])
	}
}

func TestLicenseAPIActivatePassesCardKey(t *testing.T) {
	service := &stubLicenseService{
		activateResult: map[string]any{
			"licensed": true,
		},
	}
	handler := NewHandler(testConfig(), nil, nil, service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", bytes.NewBufferString(`{"cardKey":"CCNX-TEST"}`))

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if service.activatedKey != "CCNX-TEST" {
		t.Fatalf("expected card key to be passed, got %q", service.activatedKey)
	}
}

func TestLicenseAPIActivateReturnsBadRequestOnServiceError(t *testing.T) {
	service := &stubLicenseService{activateErr: errors.New("invalid card")}
	handler := NewHandler(testConfig(), nil, nil, service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", bytes.NewBufferString(`{"cardKey":"bad"}`))

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func testConfig() *config.Config {
	return &config.Config{}
}
