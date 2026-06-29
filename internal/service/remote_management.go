package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/onlinelicense"
	"github.com/lich0821/ccNexus/internal/storage"
)

type RemoteManagementExecutor struct {
	Config    *config.Config
	Storage   *storage.SQLiteStorage
	Endpoints *EndpointService
}

func NewRemoteManagementExecutor(cfg *config.Config, store *storage.SQLiteStorage, endpoints *EndpointService) *RemoteManagementExecutor {
	return &RemoteManagementExecutor{Config: cfg, Storage: store, Endpoints: endpoints}
}

func (e *RemoteManagementExecutor) Snapshot() (onlinelicense.RemoteSnapshot, error) {
	if e == nil || e.Config == nil {
		return onlinelicense.RemoteSnapshot{}, fmt.Errorf("remote executor unavailable")
	}
	endpoints := e.Config.GetEndpoints()
	result := onlinelicense.RemoteSnapshot{
		Endpoints:  make([]onlinelicense.RemoteEndpointSnapshot, 0, len(endpoints)),
		TokenPools: []onlinelicense.RemoteTokenPoolSnapshot{},
		UpdatedAt:  time.Now().UTC(),
	}
	for _, endpoint := range endpoints {
		item := onlinelicense.RemoteEndpointSnapshot{
			Name:         endpoint.Name,
			APIUrl:       endpoint.APIUrl,
			APIKeyMasked: maskRemoteSecret(endpoint.APIKey),
			AuthMode:     endpoint.AuthMode,
			Enabled:      endpoint.Enabled,
			Transformer:  endpoint.Transformer,
			Model:        endpoint.Model,
		}
		if e.Storage != nil {
			if stats, err := e.Storage.GetEndpointTotalStats(endpoint.Name); err == nil && stats != nil {
				item.Stats = onlinelicense.RemoteUsageStats{
					Requests:     stats.Requests,
					Errors:       stats.Errors,
					InputTokens:  int(stats.InputTokens),
					OutputTokens: int(stats.OutputTokens),
				}
			}
		}
		result.Endpoints = append(result.Endpoints, item)
		authMode := config.NormalizeAuthMode(endpoint.AuthMode)
		if config.IsTokenPoolAuthMode(authMode) && e.Storage != nil {
			pool := onlinelicense.RemoteTokenPoolSnapshot{
				EndpointName: endpoint.Name,
				AuthMode:     authMode,
				Credentials:  []onlinelicense.RemoteCredentialSnapshot{},
			}
			credentials, err := e.Storage.GetEndpointCredentials(endpoint.Name)
			if err == nil {
				usage, _ := e.Storage.GetCredentialUsageByEndpoint(endpoint.Name)
				rateLimits, _ := e.Storage.GetCredentialRateLimitsByEndpoint(endpoint.Name)
				for _, cred := range credentials {
					entry := onlinelicense.RemoteCredentialSnapshot{
						ID:              cred.ID,
						AccountIDMasked: maskRemoteSecret(cred.AccountID),
						EmailMasked:     maskRemoteEmail(cred.Email),
						Status:          cred.Status,
						Enabled:         cred.Enabled,
					}
					if usage != nil && usage[cred.ID] != nil {
						entry.Usage = onlinelicense.RemoteUsageStats{
							Requests:     usage[cred.ID].Requests,
							Errors:       usage[cred.ID].Errors,
							InputTokens:  usage[cred.ID].InputTokens,
							OutputTokens: usage[cred.ID].OutputTokens,
						}
					}
					if rateLimits != nil && rateLimits[cred.ID] != nil {
						entry.Quota = rateLimits[cred.ID]
					}
					pool.Credentials = append(pool.Credentials, entry)
				}
			}
			result.TokenPools = append(result.TokenPools, pool)
		}
	}
	return result, nil
}

func (e *RemoteManagementExecutor) ExecuteRemoteCommand(command onlinelicense.RemoteCommandPayload) (*onlinelicense.RemoteExecutionOutcome, error) {
	commandType := strings.TrimSpace(command.CommandType)
	outcome := &onlinelicense.RemoteExecutionOutcome{}
	switch commandType {
	case "endpoint.create":
		if err := e.executeEndpointCreate(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "endpoint.update":
		if err := e.executeEndpointUpdate(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "endpoint.delete":
		if err := e.executeEndpointDelete(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "endpoint.reorder":
		if err := e.executeEndpointReorder(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "credential.setEnabled":
		if err := e.executeCredentialEnabled(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "credential.updateToken":
		if err := e.executeCredentialUpdateToken(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "credential.delete":
		if err := e.executeCredentialDelete(command.Payload); err != nil {
			return nil, err
		}
		outcome.ConfigChanged = true
	case "secret.reveal":
		reveal, err := e.executeSecretReveal(command.Payload)
		if err != nil {
			return nil, err
		}
		outcome.SecretReveal = reveal
		outcome.SecretRevealReady = reveal != nil
	default:
		return nil, fmt.Errorf("unsupported remote command %q", commandType)
	}
	snapshot, err := e.Snapshot()
	if err != nil {
		return nil, err
	}
	outcome.Snapshot = &snapshot
	outcome.SnapshotUpdated = true
	outcome.Message = "ok"
	return outcome, nil
}

func (e *RemoteManagementExecutor) executeEndpointCreate(raw json.RawMessage) error {
	var req struct {
		Name        string `json:"name"`
		APIUrl      string `json:"apiUrl"`
		APIKey      string `json:"apiKey"`
		AuthMode    string `json:"authMode"`
		Transformer string `json:"transformer"`
		Model       string `json:"model"`
		Thinking    string `json:"thinking"`
		ProxyURL    string `json:"proxyUrl"`
		ForceStream *bool  `json:"forceStream"`
		Remark      string `json:"remark"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	if e.Endpoints == nil {
		return fmt.Errorf("endpoint service unavailable")
	}
	forceStream := false
	if req.ForceStream != nil {
		forceStream = *req.ForceStream
	}
	if err := e.Endpoints.AddEndpoint(req.Name, req.APIUrl, req.APIKey, req.AuthMode, req.Transformer, req.Model, req.Thinking, req.ProxyURL, forceStream, req.Remark); err != nil {
		return err
	}
	if req.Enabled != nil && !*req.Enabled {
		index, _, err := e.endpointByName(req.Name)
		if err != nil {
			return err
		}
		return e.Endpoints.ToggleEndpoint(index, false)
	}
	return nil
}

func (e *RemoteManagementExecutor) executeEndpointUpdate(raw json.RawMessage) error {
	var req struct {
		EndpointName string  `json:"endpointName"`
		Name         *string `json:"name"`
		APIUrl       *string `json:"apiUrl"`
		APIKey       *string `json:"apiKey"`
		AuthMode     *string `json:"authMode"`
		Transformer  *string `json:"transformer"`
		Model        *string `json:"model"`
		Thinking     *string `json:"thinking"`
		ProxyURL     *string `json:"proxyUrl"`
		ForceStream  *bool   `json:"forceStream"`
		Remark       *string `json:"remark"`
		Enabled      *bool   `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	index, endpoint, err := e.endpointByName(req.EndpointName)
	if err != nil {
		return err
	}
	name := endpoint.Name
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		name = strings.TrimSpace(*req.Name)
	}
	apiURL := endpoint.APIUrl
	if req.APIUrl != nil {
		apiURL = strings.TrimSpace(*req.APIUrl)
	}
	apiKey := endpoint.APIKey
	if req.APIKey != nil {
		apiKey = strings.TrimSpace(*req.APIKey)
	}
	authMode := endpoint.AuthMode
	if req.AuthMode != nil && strings.TrimSpace(*req.AuthMode) != "" {
		authMode = strings.TrimSpace(*req.AuthMode)
	}
	transformer := endpoint.Transformer
	if req.Transformer != nil && strings.TrimSpace(*req.Transformer) != "" {
		transformer = strings.TrimSpace(*req.Transformer)
	}
	model := endpoint.Model
	if req.Model != nil {
		model = strings.TrimSpace(*req.Model)
	}
	thinking := endpoint.Thinking
	if req.Thinking != nil {
		thinking = strings.TrimSpace(*req.Thinking)
	}
	proxyURL := endpoint.ProxyURL
	if req.ProxyURL != nil {
		proxyURL = strings.TrimSpace(*req.ProxyURL)
	}
	remark := endpoint.Remark
	if req.Remark != nil {
		remark = strings.TrimSpace(*req.Remark)
	}
	forceStream := endpoint.ForceStream
	if req.ForceStream != nil {
		forceStream = *req.ForceStream
	}
	enabled := endpoint.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if e.Endpoints == nil {
		return fmt.Errorf("endpoint service unavailable")
	}
	if err := e.Endpoints.UpdateEndpoint(index, name, apiURL, apiKey, authMode, transformer, model, thinking, proxyURL, forceStream, remark); err != nil {
		return err
	}
	return e.Endpoints.ToggleEndpoint(index, enabled)
}

func (e *RemoteManagementExecutor) executeSecretReveal(raw json.RawMessage) (*onlinelicense.RemoteSecretRevealResult, error) {
	var req onlinelicense.RemoteSecretRevealPayload
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	field := strings.TrimSpace(req.Field)
	if field == "" {
		return nil, fmt.Errorf("secret field is required")
	}
	if strings.TrimSpace(req.AdminPublicKey) == "" {
		return nil, fmt.Errorf("admin public key is required")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.ExpiresAt))
	if err != nil {
		return nil, fmt.Errorf("invalid reveal expiry: %w", err)
	}
	if !expiresAt.IsZero() && !time.Now().UTC().Before(expiresAt.UTC()) {
		return nil, fmt.Errorf("secret reveal command expired")
	}
	value, endpointName, credentialID, err := e.remoteSecretValue(req)
	if err != nil {
		return nil, err
	}
	return onlinelicense.EncryptRemoteSecretRevealResult(req.AdminPublicKey, onlinelicense.RemoteSecretRevealPlaintext{
		EndpointName: endpointName,
		CredentialID: credentialID,
		Field:        field,
		Value:        value,
	}, expiresAt.UTC())
}

func (e *RemoteManagementExecutor) remoteSecretValue(req onlinelicense.RemoteSecretRevealPayload) (string, string, int64, error) {
	field := strings.TrimSpace(req.Field)
	if req.CredentialID > 0 {
		cred, err := e.credentialByID(req.CredentialID)
		if err != nil {
			return "", "", 0, err
		}
		switch field {
		case "accessToken":
			return cred.AccessToken, cred.EndpointName, cred.ID, nil
		case "refreshToken":
			return cred.RefreshToken, cred.EndpointName, cred.ID, nil
		case "idToken":
			return cred.IDToken, cred.EndpointName, cred.ID, nil
		default:
			return "", "", 0, fmt.Errorf("unsupported credential secret field %q", field)
		}
	}
	_, endpoint, err := e.endpointByName(req.EndpointName)
	if err != nil {
		return "", "", 0, err
	}
	switch field {
	case "apiKey":
		return endpoint.APIKey, endpoint.Name, 0, nil
	default:
		return "", "", 0, fmt.Errorf("unsupported endpoint secret field %q", field)
	}
}

func (e *RemoteManagementExecutor) executeEndpointDelete(raw json.RawMessage) error {
	var req struct {
		EndpointName string `json:"endpointName"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	index, _, err := e.endpointByName(req.EndpointName)
	if err != nil {
		return err
	}
	if e.Endpoints == nil {
		return fmt.Errorf("endpoint service unavailable")
	}
	return e.Endpoints.RemoveEndpoint(index)
}

func (e *RemoteManagementExecutor) executeEndpointReorder(raw json.RawMessage) error {
	var req struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	if e.Endpoints == nil {
		return fmt.Errorf("endpoint service unavailable")
	}
	names := make([]string, 0, len(req.Names))
	for _, name := range req.Names {
		names = append(names, strings.TrimSpace(name))
	}
	return e.Endpoints.ReorderEndpoints(names)
}

func (e *RemoteManagementExecutor) executeCredentialEnabled(raw json.RawMessage) error {
	var req struct {
		CredentialID int64 `json:"credentialId"`
		Enabled      bool  `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	return e.updateCredential(req.CredentialID, func(cred *storage.EndpointCredential) error {
		cred.Enabled = req.Enabled
		return nil
	})
}

func (e *RemoteManagementExecutor) executeCredentialUpdateToken(raw json.RawMessage) error {
	var req struct {
		CredentialID int64  `json:"credentialId"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		IDToken      string `json:"idToken"`
		ExpiresAt    string `json:"expiresAt"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	return e.updateCredential(req.CredentialID, func(cred *storage.EndpointCredential) error {
		if strings.TrimSpace(req.AccessToken) != "" {
			cred.AccessToken = strings.TrimSpace(req.AccessToken)
		}
		if strings.TrimSpace(req.RefreshToken) != "" {
			cred.RefreshToken = strings.TrimSpace(req.RefreshToken)
		}
		if strings.TrimSpace(req.IDToken) != "" {
			cred.IDToken = strings.TrimSpace(req.IDToken)
		}
		if strings.TrimSpace(req.ExpiresAt) != "" {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
			if err != nil {
				return err
			}
			parsed = parsed.UTC()
			cred.ExpiresAt = &parsed
		}
		cred.Status = "active"
		cred.FailureCount = 0
		cred.CooldownUntil = nil
		cred.LastError = ""
		return nil
	})
}

func (e *RemoteManagementExecutor) executeCredentialDelete(raw json.RawMessage) error {
	var req struct {
		CredentialID int64 `json:"credentialId"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	cred, err := e.credentialByID(req.CredentialID)
	if err != nil {
		return err
	}
	return e.Storage.DeleteEndpointCredential(cred.EndpointName, cred.ID)
}

func (e *RemoteManagementExecutor) endpointByName(name string) (int, config.Endpoint, error) {
	if e == nil || e.Config == nil {
		return 0, config.Endpoint{}, fmt.Errorf("config unavailable")
	}
	name = strings.TrimSpace(name)
	for index, endpoint := range e.Config.GetEndpoints() {
		if endpoint.Name == name {
			return index, endpoint, nil
		}
	}
	return 0, config.Endpoint{}, fmt.Errorf("endpoint not found")
}

func (e *RemoteManagementExecutor) credentialByID(id int64) (*storage.EndpointCredential, error) {
	if e == nil || e.Storage == nil {
		return nil, fmt.Errorf("storage unavailable")
	}
	cred, err := e.Storage.GetCredentialByID(id)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("credential not found")
	}
	return cred, nil
}

func (e *RemoteManagementExecutor) updateCredential(id int64, update func(*storage.EndpointCredential) error) error {
	cred, err := e.credentialByID(id)
	if err != nil {
		return err
	}
	if err := update(cred); err != nil {
		return err
	}
	return e.Storage.UpdateEndpointCredential(cred)
}

func maskRemoteSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "***"
	}
	return value[:4] + "***" + value[len(value)-4:]
}

func maskRemoteEmail(value string) string {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, "@")
	if len(parts) != 2 || parts[0] == "" {
		return maskRemoteSecret(value)
	}
	return parts[0][:1] + "***@" + parts[1]
}
