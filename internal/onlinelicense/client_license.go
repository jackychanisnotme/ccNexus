package onlinelicense

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	clientStateConfigKey     = "online_license_state"
	clientRemoteKeyConfigKey = "online_license_remote_x25519_private_key"
	licenseServerURLCooldown = time.Minute
)

var AppPublicKey string

type ConfigStore interface {
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
}

type ClientService struct {
	store              ConfigStore
	deviceID           string
	serverURL          string
	serverURLs         []string
	serverURLCooldowns map[string]time.Time
	serverURLMu        sync.Mutex
	verifier           *Service
	client             *http.Client
	version            string
	now                func() time.Time
	remote             RemoteExecutor
	remoteKey          *ecdh.PrivateKey
}

type ClientOptions struct {
	ServerURL  string
	ServerURLs []string
	PublicKey  ed25519.PublicKey
	Version    string
	Now        func() time.Time
	Client     *http.Client
}

type RemoteExecutor interface {
	Snapshot() (RemoteSnapshot, error)
	ExecuteRemoteCommand(command RemoteCommandPayload) (*RemoteExecutionOutcome, error)
}

type ClientState struct {
	Ticket        string    `json:"ticket"`
	LicenseID     int64     `json:"licenseId"`
	ActivationID  int64     `json:"activationId"`
	DeviceID      string    `json:"deviceId"`
	Plan          Plan      `json:"plan,omitempty"`
	Status        string    `json:"status,omitempty"`
	ExpiresAt     time.Time `json:"expiresAt"`
	NextCheckAt   time.Time `json:"nextCheckAt"`
	GraceUntil    time.Time `json:"graceUntil"`
	LastCheckedAt time.Time `json:"lastCheckedAt"`
	Message       string    `json:"message,omitempty"`
}

func NewClientService(store ConfigStore, deviceID string, opts ClientOptions) *ClientService {
	serverURLs := configuredLicenseServerURLs(opts)
	serverURL := DefaultLicenseServerURL
	if len(serverURLs) > 0 {
		serverURL = serverURLs[0]
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
		store:              store,
		deviceID:           strings.TrimSpace(deviceID),
		serverURL:          serverURL,
		serverURLs:         serverURLs,
		serverURLCooldowns: make(map[string]time.Time),
		verifier:           NewVerifier(opts.PublicKey, Options{Now: now}),
		client:             client,
		version:            strings.TrimSpace(opts.Version),
		now:                now,
	}
}

func configuredLicenseServerURLs(opts ClientOptions) []string {
	if len(opts.ServerURLs) > 0 {
		return normalizeLicenseServerURLs(opts.ServerURLs, false)
	}
	if strings.TrimSpace(opts.ServerURL) != "" {
		return normalizeLicenseServerURLs([]string{opts.ServerURL}, true)
	}
	if raw := strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_SERVER_URLS")); raw != "" {
		return normalizeLicenseServerURLs(strings.Split(raw, ","), false)
	}
	if raw := strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_SERVER_URL")); raw != "" {
		return normalizeLicenseServerURLs([]string{raw}, true)
	}
	return normalizeLicenseServerURLs(DefaultLicenseServerURLs, false)
}

func normalizeLicenseServerURLs(values []string, addDefaultPeer bool) []string {
	result := make([]string, 0, len(values)+1)
	for _, value := range values {
		addLicenseServerURL(&result, value)
	}
	if len(result) == 0 {
		for _, value := range DefaultLicenseServerURLs {
			addLicenseServerURL(&result, value)
		}
		return result
	}
	if addDefaultPeer {
		switch result[0] {
		case DefaultLicenseServerDomainURL:
			addLicenseServerURL(&result, DefaultLicenseServerIPURL)
		case DefaultLicenseServerIPURL:
			addLicenseServerURL(&result, DefaultLicenseServerDomainURL)
		}
	}
	return result
}

func addLicenseServerURL(urls *[]string, value string) {
	clean := strings.TrimRight(strings.TrimSpace(value), "/")
	if clean == "" {
		return
	}
	for _, existing := range *urls {
		if existing == clean {
			return
		}
	}
	*urls = append(*urls, clean)
}

func (s *ClientService) SetRemoteExecutor(executor RemoteExecutor) {
	if s == nil {
		return
	}
	s.remote = executor
}

func NewConfiguredClientService(store ConfigStore, deviceID, version string) (*ClientService, error) {
	publicKeyText := strings.TrimSpace(AppPublicKey)
	if publicKeyText == "" {
		publicKeyText = os.Getenv("CCNEXUS_LICENSE_PUBLIC_KEY")
	}
	publicKey, err := PublicKeyFromString(publicKeyText)
	var serverURLs []string
	if raw := strings.TrimSpace(os.Getenv("CCNEXUS_LICENSE_SERVER_URLS")); raw != "" {
		serverURLs = strings.Split(raw, ",")
	}
	service := NewClientService(store, deviceID, ClientOptions{
		ServerURL:  os.Getenv("CCNEXUS_LICENSE_SERVER_URL"),
		ServerURLs: serverURLs,
		PublicKey:  publicKey,
		Version:    version,
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
	if isDisabledStatus(state.Status) {
		status := statusFromClientState(state)
		return &status, nil
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
	if err := s.ensureVerifier(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	var result ActivationResult
	if err := s.postJSON("/api/license/activate", ActivationRequest{
		CardKey:    cardKey,
		DeviceID:   s.deviceID,
		Platform:   runtime.GOOS,
		AppVersion: s.version,
		Remote:     s.remoteCapabilityReport(),
	}, &result); err != nil {
		return nil, err
	}
	if _, err := s.verifier.VerifyTicket(result.Ticket, s.deviceID, now); err != nil {
		return nil, err
	}
	if err := s.saveResult(&result, now); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *ClientService) ensureVerifier() error {
	if s == nil || s.verifier == nil || len(s.verifier.publicKey) != ed25519.PublicKeySize {
		return ErrInvalidTicket
	}
	return nil
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
		Remote:     s.remoteCapabilityReport(),
	}, &result); err != nil {
		if disabled, ok := s.disabledResultFromState(state, err, now); ok {
			if err := s.saveResult(disabled, now); err != nil {
				return nil, err
			}
			return disabled, nil
		}
		return nil, err
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	if result.Ticket != "" {
		if _, err := s.verifier.VerifyTicket(result.Ticket, s.deviceID, now); err != nil {
			return nil, err
		}
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

func (s *ClientService) PollRemoteOnce() (*RemotePollOutcome, error) {
	return s.pollRemote(false)
}

func (s *ClientService) PollRemoteCommandsOnly() (*RemotePollOutcome, error) {
	return s.pollRemote(true)
}

func (s *ClientService) pollRemote(commandsOnly bool) (*RemotePollOutcome, error) {
	outcome := &RemotePollOutcome{}
	if s == nil || s.remote == nil {
		return outcome, nil
	}
	state, err := s.loadState()
	if err != nil || state.Ticket == "" {
		return outcome, err
	}
	var response RemotePollResponse
	if err := s.postJSON("/api/license/remote/poll", RemotePollRequest{
		Ticket:   state.Ticket,
		DeviceID: s.deviceID,
	}, &response); err != nil {
		return outcome, err
	}
	outcome.CommandCount = len(response.Commands)
	if len(response.Commands) == 0 {
		if commandsOnly {
			return outcome, nil
		}
		snapshot, err := s.remote.Snapshot()
		if err != nil {
			return outcome, err
		}
		if err := s.submitRemoteResult(RemoteResultRequest{
			Ticket:   state.Ticket,
			DeviceID: s.deviceID,
			Status:   "snapshot",
			Snapshot: &snapshot,
		}); err != nil {
			return outcome, err
		}
		outcome.SnapshotUpdated = true
		return outcome, nil
	}
	key, err := s.ensureRemoteKey()
	if err != nil {
		return outcome, err
	}
	for _, command := range response.Commands {
		plain, err := DecryptRemoteEnvelope(key, command.Envelope)
		status := RemoteCommandStatusFailed
		var snapshot *RemoteSnapshot
		var result *RemoteCommandResult
		var secretReveal *RemoteSecretRevealResult
		if err == nil {
			var payload RemoteCommandPayload
			if decodeErr := json.Unmarshal(plain, &payload); decodeErr != nil {
				err = decodeErr
			} else {
				commandOutcome, execErr := s.remote.ExecuteRemoteCommand(payload)
				err = execErr
				if err == nil {
					status = RemoteCommandStatusApplied
					if commandOutcome != nil {
						snapshot = commandOutcome.Snapshot
						result = &RemoteCommandResult{
							Message:           commandOutcome.Message,
							ConfigChanged:     commandOutcome.ConfigChanged,
							SnapshotUpdated:   commandOutcome.SnapshotUpdated || commandOutcome.Snapshot != nil,
							SecretRevealReady: commandOutcome.SecretRevealReady,
							SecretReveal:      commandOutcome.SecretReveal,
						}
						if commandOutcome.SecretReveal != nil {
							commandOutcome.SecretReveal.CommandID = command.ID
							secretReveal = commandOutcome.SecretReveal
						}
						if snapshot != nil {
							result.SnapshotUpdatedAt = snapshot.UpdatedAt
						}
						outcome.ConfigChanged = outcome.ConfigChanged || commandOutcome.ConfigChanged
						outcome.SnapshotUpdated = outcome.SnapshotUpdated || commandOutcome.SnapshotUpdated || commandOutcome.Snapshot != nil
						outcome.SecretRevealReady = outcome.SecretRevealReady || commandOutcome.SecretRevealReady
					}
				}
			}
		}
		errorText := ""
		if err != nil {
			errorText = err.Error()
		}
		if submitErr := s.submitRemoteResult(RemoteResultRequest{
			Ticket:       state.Ticket,
			DeviceID:     s.deviceID,
			CommandID:    command.ID,
			Status:       status,
			Error:        errorText,
			Snapshot:     snapshot,
			Result:       result,
			SecretReveal: secretReveal,
		}); submitErr != nil {
			return outcome, submitErr
		}
	}
	return outcome, nil
}

func (s *ClientService) submitRemoteResult(req RemoteResultRequest) error {
	var out map[string]bool
	return s.postJSON("/api/license/remote/result", req, &out)
}

func (s *ClientService) remoteCapabilityReport() RemoteCapabilityReport {
	if s == nil || s.remote == nil {
		return RemoteCapabilityReport{}
	}
	key, err := s.ensureRemoteKey()
	if err != nil {
		return RemoteCapabilityReport{}
	}
	return RemoteCapabilityReport{
		Supported: true,
		Enabled:   true,
		PublicKey: EncodeRemotePublicKey(key.PublicKey()),
		Capabilities: []string{
			"endpoints:view",
			"endpoints:write",
			"token_pool:view",
			"token_pool:write",
			"secrets:reveal",
		},
	}
}

func (s *ClientService) ensureRemoteKey() (*ecdh.PrivateKey, error) {
	if s.remoteKey != nil {
		return s.remoteKey, nil
	}
	if s.store != nil {
		raw, err := s.store.GetConfig(clientRemoteKeyConfigKey)
		if err == nil && strings.TrimSpace(raw) != "" {
			data, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
			if decodeErr == nil {
				key, keyErr := ecdh.X25519().NewPrivateKey(data)
				if keyErr == nil {
					s.remoteKey = key
					return key, nil
				}
			}
		}
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	s.remoteKey = key
	if s.store != nil {
		_ = s.store.SetConfig(clientRemoteKeyConfigKey, base64.RawURLEncoding.EncodeToString(key.Bytes()))
	}
	return key, nil
}

func (s *ClientService) postJSON(path string, payload interface{}, out interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	urls := s.orderedServerURLs()
	var lastErr error
	for _, baseURL := range urls {
		raw, retryable, err := s.postJSONTo(baseURL, path, data)
		if err == nil {
			s.markServerURLSuccess(baseURL)
			if out == nil {
				return nil
			}
			return json.Unmarshal(raw, out)
		}
		if !retryable {
			return err
		}
		s.markServerURLFailure(baseURL)
		lastErr = err
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no license server URL configured")
}

func (s *ClientService) postJSONTo(baseURL, path string, data []byte) (json.RawMessage, bool, error) {
	resp, err := s.client.Post(baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		if isRetryableLicenseServerStatus(resp.StatusCode) {
			return nil, true, fmt.Errorf("%s", resp.Status)
		}
		return nil, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Success {
		if envelope.Error == "" {
			envelope.Error = resp.Status
		}
		return nil, isRetryableLicenseServerStatus(resp.StatusCode), fmt.Errorf("%s", envelope.Error)
	}
	return envelope.Data, false, nil
}

func isRetryableLicenseServerStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func (s *ClientService) orderedServerURLs() []string {
	if s == nil {
		return nil
	}
	now := s.currentTime()
	s.serverURLMu.Lock()
	defer s.serverURLMu.Unlock()
	source := s.serverURLs
	if len(source) == 0 && strings.TrimSpace(s.serverURL) != "" {
		source = []string{s.serverURL}
	}
	active := strings.TrimSpace(s.serverURL)
	result := make([]string, 0, len(source))
	addIfAvailable := func(url string) {
		url = strings.TrimRight(strings.TrimSpace(url), "/")
		if url == "" || serverURLInList(result, url) {
			return
		}
		if until, ok := s.serverURLCooldowns[url]; ok && now.Before(until) {
			return
		}
		result = append(result, url)
	}
	addIfAvailable(active)
	for _, url := range source {
		addIfAvailable(url)
	}
	if len(result) > 0 {
		return result
	}
	for _, url := range source {
		url = strings.TrimRight(strings.TrimSpace(url), "/")
		if url != "" && !serverURLInList(result, url) {
			result = append(result, url)
		}
	}
	return result
}

func (s *ClientService) markServerURLSuccess(baseURL string) {
	if s == nil {
		return
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return
	}
	s.serverURLMu.Lock()
	s.serverURL = baseURL
	delete(s.serverURLCooldowns, baseURL)
	s.serverURLMu.Unlock()
}

func (s *ClientService) markServerURLFailure(baseURL string) {
	if s == nil {
		return
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return
	}
	s.serverURLMu.Lock()
	if s.serverURLCooldowns == nil {
		s.serverURLCooldowns = make(map[string]time.Time)
	}
	s.serverURLCooldowns[baseURL] = s.currentTime().Add(licenseServerURLCooldown)
	s.serverURLMu.Unlock()
}

func serverURLInList(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
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
	previous, _ := s.loadState()
	ticket := result.Ticket
	if ticket == "" && previous != nil {
		ticket = previous.Ticket
	}
	licenseID := result.LicenseID
	if licenseID == 0 && previous != nil {
		licenseID = previous.LicenseID
	}
	activationID := result.ActivationID
	if activationID == 0 && previous != nil {
		activationID = previous.ActivationID
	}
	deviceID := strings.TrimSpace(result.DeviceID)
	if deviceID == "" && previous != nil {
		deviceID = previous.DeviceID
	}
	plan := result.Plan
	if plan == "" && previous != nil {
		plan = previous.Plan
	}
	status := strings.TrimSpace(result.Status)
	if status == "" && result.Licensed {
		status = ActivationStatusActive
	}
	state := ClientState{
		Ticket:        ticket,
		LicenseID:     licenseID,
		ActivationID:  activationID,
		DeviceID:      deviceID,
		Plan:          plan,
		Status:        status,
		ExpiresAt:     result.ExpiresAt,
		NextCheckAt:   result.NextCheckAt,
		GraceUntil:    result.GraceUntil,
		LastCheckedAt: now,
		Message:       strings.TrimSpace(result.Message),
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.store.SetConfig(clientStateConfigKey, string(data))
}

func (s *ClientService) disabledResultFromState(state *ClientState, err error, now time.Time) (*ActivationResult, bool) {
	message, ok := remoteDisabledMessage(err)
	if !ok || state == nil {
		return nil, false
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	return &ActivationResult{
		Licensed:     false,
		LicenseID:    state.LicenseID,
		ActivationID: state.ActivationID,
		DeviceID:     state.DeviceID,
		Plan:         state.Plan,
		Status:       ActivationStatusDisabled,
		ExpiresAt:    state.ExpiresAt,
		NextCheckAt:  state.NextCheckAt,
		GraceUntil:   state.GraceUntil,
		Message:      message,
	}, true
}

func remoteDisabledMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	message := strings.TrimSpace(err.Error())
	switch message {
	case ErrActivationBlocked.Error(), ErrCardDisabled.Error():
		return message, true
	default:
		return "", false
	}
}

func isDisabledStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), ActivationStatusDisabled) ||
		strings.EqualFold(strings.TrimSpace(status), CardStatusDisabled)
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
		LastPlan:        ticket.Plan,
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

func statusFromClientState(state *ClientState) Status {
	status := Status{
		Product:         ProductCCNexusPro,
		Licensed:        false,
		ExpiresAt:       state.ExpiresAt,
		LastPlan:        state.Plan,
		LastActivatedAt: state.NextCheckAt.Add(-nextCheckInterval),
		Message:         strings.TrimSpace(state.Message),
	}
	if status.LastActivatedAt.IsZero() || state.NextCheckAt.IsZero() {
		status.LastActivatedAt = time.Time{}
	}
	if status.Message == "" {
		status.Message = "license is disabled"
	}
	return status
}
