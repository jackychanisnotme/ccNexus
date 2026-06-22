package onlinelicense

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	clientStateConfigKey = "online_license_state"
)

var AppPublicKey string

type ConfigStore interface {
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
}

type ClientService struct {
	store     ConfigStore
	deviceID  string
	serverURL string
	verifier  *Service
	client    *http.Client
	version   string
	now       func() time.Time
}

type ClientOptions struct {
	ServerURL string
	PublicKey ed25519.PublicKey
	Version   string
	Now       func() time.Time
	Client    *http.Client
}

type ClientState struct {
	Ticket        string    `json:"ticket"`
	LicenseID     int64     `json:"licenseId"`
	ActivationID  int64     `json:"activationId"`
	DeviceID      string    `json:"deviceId"`
	ExpiresAt     time.Time `json:"expiresAt"`
	NextCheckAt   time.Time `json:"nextCheckAt"`
	GraceUntil    time.Time `json:"graceUntil"`
	LastCheckedAt time.Time `json:"lastCheckedAt"`
}

func NewClientService(store ConfigStore, deviceID string, opts ClientOptions) *ClientService {
	serverURL := strings.TrimRight(strings.TrimSpace(opts.ServerURL), "/")
	if serverURL == "" {
		serverURL = strings.TrimRight(strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_SERVER_URL")), "/")
	}
	if serverURL == "" {
		serverURL = DefaultLicenseServerURL
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	return &ClientService{
		store:     store,
		deviceID:  strings.TrimSpace(deviceID),
		serverURL: serverURL,
		verifier:  NewVerifier(opts.PublicKey, Options{Now: now}),
		client:    client,
		version:   strings.TrimSpace(opts.Version),
		now:       now,
	}
}

func NewConfiguredClientService(store ConfigStore, deviceID, version string) (*ClientService, error) {
	publicKeyText := strings.TrimSpace(AppPublicKey)
	if publicKeyText == "" {
		publicKeyText = os.Getenv("CCNEXUS_LICENSE_PUBLIC_KEY")
	}
	publicKey, err := PublicKeyFromString(publicKeyText)
	service := NewClientService(store, deviceID, ClientOptions{
		ServerURL: os.Getenv("CCNEXUS_LICENSE_SERVER_URL"),
		PublicKey: publicKey,
		Version:   version,
	})
	if err != nil {
		return service, err
	}
	return service, nil
}

func (s *ClientService) Status(now time.Time) (*Status, error) {
	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	if state.Ticket == "" {
		return &Status{Product: ProductCCNexusPro, Message: "license is not activated"}, nil
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	ticket, err := s.verifier.VerifyTicket(state.Ticket, s.deviceID, now)
	if err != nil {
		return &Status{Product: ProductCCNexusPro, Expired: true, Message: err.Error()}, nil
	}
	status := statusFromTicket(ticket, now)
	return &status, nil
}

func (s *ClientService) IsLicensed(now time.Time) bool {
	status, err := s.Status(now)
	return err == nil && status.Licensed
}

func (s *ClientService) Activate(cardKey string, now time.Time) (*ActivationResult, error) {
	if now.IsZero() {
		now = s.currentTime()
	}
	var result ActivationResult
	if err := s.postJSON("/api/license/activate", ActivationRequest{
		CardKey:    cardKey,
		DeviceID:   s.deviceID,
		Platform:   runtime.GOOS,
		AppVersion: s.version,
	}, &result); err != nil {
		return nil, err
	}
	if err := s.saveResult(&result, now); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *ClientService) Refresh(now time.Time) (*ActivationResult, error) {
	state, err := s.loadState()
	if err != nil {
		return nil, err
	}
	if state.Ticket == "" {
		return nil, ErrInvalidTicket
	}
	var result ActivationResult
	if err := s.postJSON("/api/license/refresh", RefreshRequest{
		Ticket:     state.Ticket,
		DeviceID:   s.deviceID,
		Platform:   runtime.GOOS,
		AppVersion: s.version,
	}, &result); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	if err := s.saveResult(&result, now); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *ClientService) MaybeRefresh(now time.Time) {
	if now.IsZero() {
		now = s.currentTime()
	}
	state, err := s.loadState()
	if err != nil || state.Ticket == "" || state.NextCheckAt.IsZero() || now.Before(state.NextCheckAt) {
		return
	}
	_, _ = s.Refresh(now)
}

func (s *ClientService) postJSON(path string, payload interface{}, out interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := s.client.Post(s.serverURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Success {
		if envelope.Error == "" {
			envelope.Error = resp.Status
		}
		return fmt.Errorf("%s", envelope.Error)
	}
	return json.Unmarshal(envelope.Data, out)
}

func (s *ClientService) loadState() (*ClientState, error) {
	if s.store == nil {
		return &ClientState{}, nil
	}
	raw, err := s.store.GetConfig(clientStateConfigKey)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return &ClientState{}, nil
	}
	var state ClientState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *ClientService) saveResult(result *ActivationResult, now time.Time) error {
	if s.store == nil || result == nil {
		return nil
	}
	state := ClientState{
		Ticket:        result.Ticket,
		LicenseID:     result.LicenseID,
		ActivationID:  result.ActivationID,
		DeviceID:      result.DeviceID,
		ExpiresAt:     result.ExpiresAt,
		NextCheckAt:   result.NextCheckAt,
		GraceUntil:    result.GraceUntil,
		LastCheckedAt: now,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.store.SetConfig(clientStateConfigKey, string(data))
}

func (s *ClientService) currentTime() time.Time {
	now := s.now()
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC()
}

func statusFromTicket(ticket *TicketStatus, now time.Time) Status {
	status := Status{
		Product:         ProductCCNexusPro,
		Licensed:        ticket.Licensed,
		ExpiresAt:       ticket.ExpiresAt,
		LastActivatedAt: ticket.NextCheckAt.Add(-nextCheckInterval),
		Message:         ticket.Message,
	}
	if ticket.ExpiresAt.After(now) {
		status.RemainingDays = int(ticket.ExpiresAt.Sub(now).Hours() / 24)
		if ticket.ExpiresAt.Sub(now)%(24*time.Hour) > 0 {
			status.RemainingDays++
		}
	} else {
		status.Expired = true
		status.Licensed = false
		status.Message = "license has expired"
	}
	return status
}
