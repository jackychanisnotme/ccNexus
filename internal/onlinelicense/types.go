package onlinelicense

import (
	"encoding/json"
	"time"
)

const (
	ProductCCNexusPro = "ccnexus-pro"

	CardStatusActive   = "active"
	CardStatusDisabled = "disabled"

	ActivationStatusActive   = "active"
	ActivationStatusDisabled = "disabled"

	RemoteCommandStatusQueued    = "queued"
	RemoteCommandStatusDelivered = "delivered"
	RemoteCommandStatusApplied   = "applied"
	RemoteCommandStatusFailed    = "failed"
	RemoteCommandStatusExpired   = "expired"

	DefaultLicenseServerDomainURL = "https://license.wenche.xyz"
	DefaultLicenseServerIPURL     = "http://207.57.134.147:24220"
	DefaultLicenseServerURL       = DefaultLicenseServerDomainURL

	RemoteCommandSignatureVersion = "ainexus-remote-command-v1"
)

var DefaultLicenseServerURLs = []string{DefaultLicenseServerDomainURL}

const (
	AdminLevelRoot        = 1
	AdminLevelReseller    = 2
	AdminLevelDistributor = 3

	AdminAccountStatusActive   = "active"
	AdminAccountStatusDisabled = "disabled"

	AdminRelationshipSelf     = "self"
	AdminRelationshipDownline = "downline"

	PermissionCardsView            = "cards:view"
	PermissionCardsGenerate        = "cards:generate"
	PermissionCardsDisable         = "cards:disable"
	PermissionCardsDelete          = "cards:delete"
	PermissionDevicesView          = "devices:view"
	PermissionDevicesRemark        = "devices:remark"
	PermissionDevicesExpiry        = "devices:expiry"
	PermissionDevicesRemoteView    = "devices:remote:view"
	PermissionDevicesRemoteWrite   = "devices:remote:write"
	PermissionDevicesRemoteSecrets = "devices:remote:secrets"
	PermissionActivationsView      = "activations:view"
	PermissionActivationsDisable   = "activations:disable"
	PermissionHistoryView          = "history:view"
	PermissionAccountsView         = "accounts:view"
	PermissionAccountsManage       = "accounts:manage"
)

type Plan string

const (
	PlanMonthly   Plan = "monthly"
	PlanQuarterly Plan = "quarterly"
	PlanHalfYear  Plan = "half_year"
	PlanYearly    Plan = "yearly"
	PlanCustom    Plan = "custom"
)

type Options struct {
	Now                       func() time.Time
	RemoteSecretRevealEnabled bool
	AIProvider                AIProvider
}

type GenerateCardsRequest struct {
	Plan               Plan   `json:"plan"`
	Days               int    `json:"days"`
	Count              int    `json:"count"`
	MaxDevices         int    `json:"maxDevices"`
	Customer           string `json:"customer"`
	Remark             string `json:"remark"`
	OwnerAccountID     int64  `json:"ownerAccountId,omitempty"`
	CreatedByAccountID int64  `json:"-"`
}

type GenerateCardsResult struct {
	Cards []GeneratedCard `json:"cards"`
	CSV   string          `json:"csv"`
}

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type GeneratedCard struct {
	ID                 int64     `json:"id"`
	CardKey            string    `json:"cardKey,omitempty"`
	CardHash           string    `json:"cardHash,omitempty"`
	Plan               Plan      `json:"plan"`
	Days               int       `json:"days"`
	MaxDevices         int       `json:"maxDevices"`
	Customer           string    `json:"customer,omitempty"`
	Remark             string    `json:"remark,omitempty"`
	Status             string    `json:"status"`
	OwnerAccountID     int64     `json:"ownerAccountId,omitempty"`
	CreatedByAccountID int64     `json:"createdByAccountId,omitempty"`
	OwnerUsername      string    `json:"ownerUsername,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
}

type CardRecord struct {
	ID                 int64     `json:"id"`
	CardHash           string    `json:"cardHash"`
	Plan               Plan      `json:"plan"`
	Days               int       `json:"days"`
	MaxDevices         int       `json:"maxDevices"`
	Status             string    `json:"status"`
	Customer           string    `json:"customer,omitempty"`
	Remark             string    `json:"remark,omitempty"`
	OwnerAccountID     int64     `json:"ownerAccountId,omitempty"`
	CreatedByAccountID int64     `json:"createdByAccountId,omitempty"`
	OwnerUsername      string    `json:"ownerUsername,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	DisabledAt         time.Time `json:"disabledAt,omitempty"`
	Activations        int       `json:"activations"`
}

type ActivationRequest struct {
	CardKey    string                 `json:"cardKey"`
	DeviceID   string                 `json:"deviceId"`
	Platform   string                 `json:"platform"`
	AppVersion string                 `json:"appVersion"`
	IPAddress  string                 `json:"-"`
	Remote     RemoteCapabilityReport `json:"remote,omitempty"`
}

type RefreshRequest struct {
	Ticket     string                 `json:"ticket"`
	DeviceID   string                 `json:"deviceId"`
	Platform   string                 `json:"platform"`
	AppVersion string                 `json:"appVersion"`
	IPAddress  string                 `json:"-"`
	Remote     RemoteCapabilityReport `json:"remote,omitempty"`
}

type ActivationResult struct {
	Licensed      bool      `json:"licensed"`
	LicenseID     int64     `json:"licenseId"`
	ActivationID  int64     `json:"activationId"`
	DeviceID      string    `json:"deviceId"`
	Plan          Plan      `json:"plan,omitempty"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expiresAt"`
	RemainingDays int       `json:"remainingDays"`
	NextCheckAt   time.Time `json:"nextCheckAt"`
	GraceUntil    time.Time `json:"graceUntil"`
	Ticket        string    `json:"ticket"`
	Message       string    `json:"message"`
}

type ActivationRecord struct {
	ID             int64     `json:"id"`
	CardID         int64     `json:"cardId"`
	CardStatus     string    `json:"cardStatus"`
	Plan           Plan      `json:"plan"`
	Days           int       `json:"days"`
	DeviceID       string    `json:"deviceId"`
	Status         string    `json:"status"`
	ActivatedAt    time.Time `json:"activatedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	LastCheckedAt  time.Time `json:"lastCheckedAt"`
	DisabledAt     time.Time `json:"disabledAt,omitempty"`
	Platform       string    `json:"platform,omitempty"`
	AppVersion     string    `json:"appVersion,omitempty"`
	IPAddress      string    `json:"ipAddress,omitempty"`
	Customer       string    `json:"customer,omitempty"`
	Remark         string    `json:"remark,omitempty"`
	OwnerAccountID int64     `json:"ownerAccountId,omitempty"`
	OwnerUsername  string    `json:"ownerUsername,omitempty"`
}

type DeviceRecord struct {
	DeviceID            string             `json:"deviceId"`
	OwnerAccountID      int64              `json:"ownerAccountId,omitempty"`
	OwnerUsername       string             `json:"ownerUsername,omitempty"`
	Remark              string             `json:"remark,omitempty"`
	Status              string             `json:"status"`
	ExpiresAt           time.Time          `json:"expiresAt"`
	LastCheckedAt       time.Time          `json:"lastCheckedAt"`
	Platform            string             `json:"platform,omitempty"`
	AppVersion          string             `json:"appVersion,omitempty"`
	IPAddress           string             `json:"ipAddress,omitempty"`
	CurrentActivationID int64              `json:"currentActivationId"`
	Licenses            []ActivationRecord `json:"licenses"`
}

type AdminAccount struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"displayName,omitempty"`
	Level        int       `json:"level"`
	ParentID     int64     `json:"parentId,omitempty"`
	Relationship string    `json:"relationship,omitempty"`
	Status       string    `json:"status"`
	Permissions  []string  `json:"permissions"`
	CreatedBy    int64     `json:"createdBy,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type CreateAdminAccountRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	DisplayName string   `json:"displayName"`
	Level       int      `json:"level"`
	ParentID    int64    `json:"parentId"`
	Permissions []string `json:"permissions"`
}

type UpdateAdminAccountRequest struct {
	Password    string   `json:"password"`
	DisplayName string   `json:"displayName"`
	Level       int      `json:"level"`
	ParentID    int64    `json:"parentId"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`

	hasLevel       bool
	hasParentID    bool
	hasStatus      bool
	hasPermissions bool
}

type AdminSessionInfo struct {
	Username    string       `json:"username"`
	Account     AdminAccount `json:"account"`
	Permissions []string     `json:"permissions"`
	ExpiresAt   time.Time    `json:"expiresAt"`
}

type SetDeviceExpiryRequest struct {
	DeviceID  string    `json:"deviceId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type SetDeviceRemarkRequest struct {
	DeviceID string `json:"deviceId"`
	Remark   string `json:"remark"`
}

type RemoteCapabilityReport struct {
	Supported    bool     `json:"supported"`
	Enabled      bool     `json:"enabled"`
	PublicKey    string   `json:"publicKey,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type RemoteDeviceState struct {
	DeviceID             string         `json:"deviceId"`
	Supported            bool           `json:"supported"`
	Enabled              bool           `json:"enabled"`
	ClientVersion        string         `json:"clientVersion,omitempty"`
	Capabilities         []string       `json:"capabilities,omitempty"`
	DevicePublicKey      string         `json:"-"`
	LastHeartbeatAt      time.Time      `json:"lastHeartbeatAt,omitempty"`
	LastActivationID     int64          `json:"lastActivationId,omitempty"`
	OwnerAccountID       int64          `json:"ownerAccountId,omitempty"`
	OwnerUsername        string         `json:"ownerUsername,omitempty"`
	LastSnapshotStatus   string         `json:"lastSnapshotStatus,omitempty"`
	LastSnapshotAt       time.Time      `json:"lastSnapshotAt,omitempty"`
	LastCommandStatus    string         `json:"lastCommandStatus,omitempty"`
	LastCommandUpdatedAt time.Time      `json:"lastCommandUpdatedAt,omitempty"`
	Snapshot             RemoteSnapshot `json:"snapshot"`
}

type RemoteSnapshot struct {
	Endpoints  []RemoteEndpointSnapshot  `json:"endpoints"`
	TokenPools []RemoteTokenPoolSnapshot `json:"tokenPools"`
	UpdatedAt  time.Time                 `json:"updatedAt,omitempty"`
}

type RemoteEndpointSnapshot struct {
	Name                  string           `json:"name"`
	APIUrl                string           `json:"apiUrl"`
	APIKeyMasked          string           `json:"apiKeyMasked,omitempty"`
	AuthMode              string           `json:"authMode"`
	Enabled               bool             `json:"enabled"`
	Transformer           string           `json:"transformer,omitempty"`
	Model                 string           `json:"model,omitempty"`
	Thinking              string           `json:"thinking,omitempty"`
	CodexFastMode         bool             `json:"codexFastMode,omitempty"`
	MaxConcurrentRequests int              `json:"maxConcurrentRequests,omitempty"`
	Stats                 RemoteUsageStats `json:"stats"`
}

type RemoteTokenPoolSnapshot struct {
	EndpointName string                     `json:"endpointName"`
	AuthMode     string                     `json:"authMode,omitempty"`
	Credentials  []RemoteCredentialSnapshot `json:"credentials"`
}

type RemoteCredentialSnapshot struct {
	ID              int64            `json:"id"`
	AccountIDMasked string           `json:"accountIdMasked,omitempty"`
	EmailMasked     string           `json:"emailMasked,omitempty"`
	Status          string           `json:"status"`
	Enabled         bool             `json:"enabled"`
	Usage           RemoteUsageStats `json:"usage"`
	Quota           interface{}      `json:"quota,omitempty"`
}

type RemoteUsageStats struct {
	Requests     int `json:"requests"`
	Errors       int `json:"errors"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

type EndpointErrorTelemetryItem struct {
	EndpointName        string    `json:"endpointName"`
	EndpointFingerprint string    `json:"endpointFingerprint"`
	APIHost             string    `json:"apiHost,omitempty"`
	APIURLFingerprint   string    `json:"apiUrlFingerprint,omitempty"`
	AuthMode            string    `json:"authMode,omitempty"`
	Transformer         string    `json:"transformer,omitempty"`
	Model               string    `json:"model,omitempty"`
	Reason              string    `json:"reason"`
	StatusCode          int       `json:"statusCode,omitempty"`
	Count               int       `json:"count"`
	FirstAt             time.Time `json:"firstAt"`
	LastAt              time.Time `json:"lastAt"`
	WindowStart         time.Time `json:"windowStart"`
	WindowEnd           time.Time `json:"windowEnd"`
	Sample              string    `json:"sample,omitempty"`
}

type EndpointErrorTelemetryLocalRecord struct {
	ID int64 `json:"-"`
	EndpointErrorTelemetryItem
}

type EndpointErrorTelemetryRequest struct {
	Ticket      string                       `json:"ticket"`
	DeviceID    string                       `json:"deviceId"`
	Platform    string                       `json:"platform,omitempty"`
	AppVersion  string                       `json:"appVersion,omitempty"`
	WindowStart time.Time                    `json:"windowStart,omitempty"`
	WindowEnd   time.Time                    `json:"windowEnd,omitempty"`
	Items       []EndpointErrorTelemetryItem `json:"items"`
}

type EndpointErrorTelemetryResult struct {
	Accepted int `json:"accepted"`
}

type EndpointErrorTelemetryQuery struct {
	DeviceID      string
	From          time.Time
	To            time.Time
	EndpointName  string
	Reason        string
	StatusCode    int
	StatusCodeSet bool
	Limit         int
}

type EndpointErrorTelemetryRecord struct {
	ID                  int64     `json:"id,omitempty"`
	DeviceID            string    `json:"deviceId,omitempty"`
	ActivationID        int64     `json:"activationId,omitempty"`
	OwnerAccountID      int64     `json:"ownerAccountId,omitempty"`
	Platform            string    `json:"platform,omitempty"`
	AppVersion          string    `json:"appVersion,omitempty"`
	EndpointName        string    `json:"endpointName"`
	EndpointFingerprint string    `json:"endpointFingerprint"`
	APIHost             string    `json:"apiHost,omitempty"`
	APIURLFingerprint   string    `json:"apiUrlFingerprint,omitempty"`
	AuthMode            string    `json:"authMode,omitempty"`
	Transformer         string    `json:"transformer,omitempty"`
	Model               string    `json:"model,omitempty"`
	Reason              string    `json:"reason"`
	StatusCode          int       `json:"statusCode,omitempty"`
	Count               int       `json:"count"`
	FirstAt             time.Time `json:"firstAt"`
	LastAt              time.Time `json:"lastAt"`
	WindowStart         time.Time `json:"windowStart"`
	WindowEnd           time.Time `json:"windowEnd"`
	Sample              string    `json:"sample,omitempty"`
	CreatedAt           time.Time `json:"createdAt,omitempty"`
	UpdatedAt           time.Time `json:"updatedAt,omitempty"`
}

type EndpointErrorTelemetrySummary struct {
	DeviceID            string    `json:"deviceId,omitempty"`
	EndpointName        string    `json:"endpointName"`
	EndpointFingerprint string    `json:"endpointFingerprint"`
	APIHost             string    `json:"apiHost,omitempty"`
	Reason              string    `json:"reason"`
	StatusCode          int       `json:"statusCode,omitempty"`
	Count               int       `json:"count"`
	LastAt              time.Time `json:"lastAt"`
	Sample              string    `json:"sample,omitempty"`
}

type EndpointErrorTelemetryResponse struct {
	DeviceID   string                          `json:"deviceId,omitempty"`
	Platform   string                          `json:"platform,omitempty"`
	AppVersion string                          `json:"appVersion,omitempty"`
	From       time.Time                       `json:"from,omitempty"`
	To         time.Time                       `json:"to,omitempty"`
	Items      []EndpointErrorTelemetryRecord  `json:"items"`
	Summary    []EndpointErrorTelemetrySummary `json:"summary,omitempty"`
}

type RemoteAdminDetail struct {
	State    RemoteDeviceState     `json:"state"`
	Commands []RemoteCommandRecord `json:"commands"`
}

type RemoteCommandRequest struct {
	CommandType string      `json:"commandType"`
	Payload     interface{} `json:"payload"`
	ExpiresAt   time.Time   `json:"-"`
}

type RemoteCommandPayload struct {
	CommandType string          `json:"commandType"`
	Payload     json.RawMessage `json:"payload"`
	QueuedAt    string          `json:"queuedAt,omitempty"`
}

type RemoteSecretRevealRequest struct {
	EndpointName   string `json:"endpointName"`
	CredentialID   int64  `json:"credentialId,omitempty"`
	Field          string `json:"field"`
	AdminPublicKey string `json:"adminPublicKey,omitempty"`
}

type RemoteSecretRevealPayload struct {
	EndpointName   string `json:"endpointName"`
	CredentialID   int64  `json:"credentialId,omitempty"`
	Field          string `json:"field"`
	AdminPublicKey string `json:"adminPublicKey"`
	ExpiresAt      string `json:"expiresAt"`
}

type RemoteSecretRevealPlaintext struct {
	EndpointName string `json:"endpointName"`
	CredentialID int64  `json:"credentialId,omitempty"`
	Field        string `json:"field"`
	Value        string `json:"value"`
}

type RemoteSecretRevealResult struct {
	CommandID       int64     `json:"commandId,omitempty"`
	Algorithm       string    `json:"algorithm"`
	ClientPublicKey string    `json:"clientPublicKey"`
	Nonce           string    `json:"nonce"`
	Ciphertext      string    `json:"ciphertext"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type RemoteCommandResult struct {
	Message           string                    `json:"message,omitempty"`
	ConfigChanged     bool                      `json:"configChanged,omitempty"`
	SnapshotUpdated   bool                      `json:"snapshotUpdated,omitempty"`
	SnapshotUpdatedAt time.Time                 `json:"snapshotUpdatedAt,omitempty"`
	SecretRevealReady bool                      `json:"secretRevealReady,omitempty"`
	SecretReveal      *RemoteSecretRevealResult `json:"secretReveal,omitempty"`
}

type RemoteExecutionOutcome struct {
	Snapshot          *RemoteSnapshot           `json:"snapshot,omitempty"`
	Message           string                    `json:"message,omitempty"`
	ConfigChanged     bool                      `json:"configChanged,omitempty"`
	SnapshotUpdated   bool                      `json:"snapshotUpdated,omitempty"`
	SecretRevealReady bool                      `json:"secretRevealReady,omitempty"`
	SecretReveal      *RemoteSecretRevealResult `json:"secretReveal,omitempty"`
}

type RemotePollOutcome struct {
	CommandCount      int  `json:"commandCount"`
	ConfigChanged     bool `json:"configChanged,omitempty"`
	SnapshotUpdated   bool `json:"snapshotUpdated,omitempty"`
	SecretRevealReady bool `json:"secretRevealReady,omitempty"`
}

type RemoteCommandRecord struct {
	ID           int64                 `json:"id"`
	DeviceID     string                `json:"deviceId"`
	CommandType  string                `json:"commandType"`
	Status       string                `json:"status"`
	CommandNonce string                `json:"commandNonce,omitempty"`
	Signature    string                `json:"signature,omitempty"`
	ActorID      int64                 `json:"actorId,omitempty"`
	ActorName    string                `json:"actorName,omitempty"`
	Summary      *RemoteCommandSummary `json:"summary,omitempty"`
	Envelope     RemoteEnvelope        `json:"envelope,omitempty"`
	Result       string                `json:"result,omitempty"`
	ResultJSON   *RemoteCommandResult  `json:"resultJson,omitempty"`
	Error        string                `json:"error,omitempty"`
	ExpiresAt    time.Time             `json:"expiresAt,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	UpdatedAt    time.Time             `json:"updatedAt"`
}

type RemoteCommandSummary struct {
	TargetType    string   `json:"targetType,omitempty"`
	TargetName    string   `json:"targetName,omitempty"`
	CredentialID  int64    `json:"credentialId,omitempty"`
	ChangedFields []string `json:"changedFields,omitempty"`
	RiskLevel     string   `json:"riskLevel,omitempty"`
}

type RemoteEnvelope struct {
	ServerPublicKey string `json:"serverPublicKey"`
	Nonce           string `json:"nonce"`
	Ciphertext      string `json:"ciphertext"`
}

type RemotePollRequest struct {
	Ticket   string `json:"ticket"`
	DeviceID string `json:"deviceId"`
}

type RemotePollResponse struct {
	Commands []RemoteCommandRecord `json:"commands"`
}

type RemoteResultRequest struct {
	Ticket       string                    `json:"ticket"`
	DeviceID     string                    `json:"deviceId"`
	CommandID    int64                     `json:"commandId"`
	Status       string                    `json:"status"`
	Error        string                    `json:"error,omitempty"`
	Snapshot     *RemoteSnapshot           `json:"snapshot,omitempty"`
	Result       *RemoteCommandResult      `json:"result,omitempty"`
	SecretReveal *RemoteSecretRevealResult `json:"secretReveal,omitempty"`
}

type AuditRecord struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	TargetType string    `json:"targetType"`
	TargetID   int64     `json:"targetId"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type TicketStatus struct {
	Licensed     bool      `json:"licensed"`
	LicenseID    int64     `json:"licenseId"`
	ActivationID int64     `json:"activationId"`
	DeviceID     string    `json:"deviceId"`
	Plan         Plan      `json:"plan,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt"`
	NextCheckAt  time.Time `json:"nextCheckAt"`
	GraceUntil   time.Time `json:"graceUntil"`
	Message      string    `json:"message"`
}

type Status struct {
	Product          string             `json:"product"`
	Licensed         bool               `json:"licensed"`
	Expired          bool               `json:"expired"`
	ExpiresAt        time.Time          `json:"expiresAt,omitempty"`
	RemainingDays    int                `json:"remainingDays"`
	LastActivatedAt  time.Time          `json:"lastActivatedAt,omitempty"`
	LastPlan         Plan               `json:"lastPlan,omitempty"`
	LastCardID       string             `json:"lastCardId,omitempty"`
	ActivationLedger []ActivationRecord `json:"activationLedger,omitempty"`
	Message          string             `json:"message,omitempty"`
}
