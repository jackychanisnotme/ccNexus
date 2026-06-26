package onlinelicense

import "time"

const (
	ProductCCNexusPro = "ccnexus-pro"

	CardStatusActive   = "active"
	CardStatusDisabled = "disabled"

	ActivationStatusActive   = "active"
	ActivationStatusDisabled = "disabled"

	DefaultLicenseServerURL = "http://207.57.134.147:24220"
)

const (
	AdminLevelRoot        = 1
	AdminLevelReseller    = 2
	AdminLevelDistributor = 3

	AdminAccountStatusActive   = "active"
	AdminAccountStatusDisabled = "disabled"

	AdminRelationshipSelf     = "self"
	AdminRelationshipDownline = "downline"

	PermissionCardsView          = "cards:view"
	PermissionCardsGenerate      = "cards:generate"
	PermissionCardsDisable       = "cards:disable"
	PermissionCardsDelete        = "cards:delete"
	PermissionDevicesView        = "devices:view"
	PermissionDevicesRemark      = "devices:remark"
	PermissionDevicesExpiry      = "devices:expiry"
	PermissionActivationsView    = "activations:view"
	PermissionActivationsDisable = "activations:disable"
	PermissionHistoryView        = "history:view"
	PermissionAccountsView       = "accounts:view"
	PermissionAccountsManage     = "accounts:manage"
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
	Now func() time.Time
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
	CardKey    string `json:"cardKey"`
	DeviceID   string `json:"deviceId"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	IPAddress  string `json:"-"`
}

type RefreshRequest struct {
	Ticket     string `json:"ticket"`
	DeviceID   string `json:"deviceId"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	IPAddress  string `json:"-"`
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
