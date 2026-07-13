package onlinelicense

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cardPrefix            = "CCNX-ONL-"
	nextCheckInterval     = 24 * time.Hour
	offlineGracePeriod    = 30 * 24 * time.Hour
	remoteSecretRevealTTL = 2 * time.Minute
	remoteCommandTTL      = 5 * time.Minute
)

var (
	ErrInvalidCard         = errors.New("invalid license card")
	ErrCardDisabled        = errors.New("license card is disabled")
	ErrDeviceLimit         = errors.New("license card device limit reached")
	ErrActivationBlocked   = errors.New("license activation is disabled")
	ErrInvalidTicket       = errors.New("invalid license ticket")
	ErrTicketExpired       = errors.New("license ticket grace period expired")
	ErrForbidden           = errors.New("forbidden")
	ErrRemoteResultExpired = errors.New("remote command result expired")
)

var (
	endpointTelemetrySecretPattern   = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{4,}|bearer\s+[A-Za-z0-9._~+/=-]+|eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
	endpointTelemetryURLQueryPattern = regexp.MustCompile(`(https?://[^\s?]+)\?[^\s]+`)
	endpointTelemetryKVSecretPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|key)=([^&\s]+)`)
)

type Store interface {
	CreateCard(card *CardRecord) error
	FindCardByHash(hash string) (*CardRecord, error)
	GetCard(id int64) (*CardRecord, error)
	ListCards() ([]CardRecord, error)
	ListCardsByOwner(ownerIDs []int64) ([]CardRecord, error)
	DisableCard(id int64, now time.Time) error
	DeleteCard(id int64) error
	ActivateCard(cardHash, deviceID string, now time.Time, platform, appVersion, ipAddress string) (*ActivationRecord, error)
	ActiveActivationCount(cardID int64) (int, error)
	FindActivation(cardID int64, deviceID string) (*ActivationRecord, error)
	LatestActivationForDevice(deviceID string) (*ActivationRecord, error)
	GetActivation(id int64) (*ActivationRecord, error)
	ListActivations() ([]ActivationRecord, error)
	ListActivationsByOwner(ownerIDs []int64) ([]ActivationRecord, error)
	UpsertActivation(activation *ActivationRecord) error
	TouchActivation(id int64, now time.Time, platform, appVersion, ipAddress string) error
	DisableActivation(id int64, now time.Time) error
	SetDeviceExpiry(deviceID string, expiresAt, now time.Time) error
	ListDeviceRemarks() (map[string]string, error)
	SetDeviceRemark(deviceID, remark string, now time.Time) error
	AddAudit(action, targetType string, targetID int64, detail string, now time.Time) error
	ListAudit(limit int) ([]AuditRecord, error)
	UpsertAdminAccount(account *AdminAccount, passwordHash string) error
	GetAdminAccount(id int64) (*AdminAccount, string, error)
	GetAdminAccountByUsername(username string) (*AdminAccount, string, error)
	ListAdminAccounts() ([]AdminAccount, error)
	UpdateAdminAccount(account *AdminAccount, passwordHash string) error
	ClaimUnownedCards(ownerID int64) error
	UpsertRemoteDeviceState(state *RemoteDeviceState, now time.Time) error
	UpsertRemoteSnapshot(deviceID string, snapshot RemoteSnapshot, now time.Time) error
	GetRemoteDevice(deviceID string) (*RemoteDeviceState, error)
	CreateRemoteCommand(command *RemoteCommandRecord, ownerAccountID int64) error
	ListRemoteCommands(deviceID string, limit int) ([]RemoteCommandRecord, error)
	ListQueuedRemoteCommands(deviceID string, limit int) ([]RemoteCommandRecord, error)
	GetRemoteCommand(deviceID string, commandID int64) (*RemoteCommandRecord, error)
	UpdateRemoteCommandSignature(deviceID string, commandID int64, commandNonce, signature string, now time.Time) error
	MarkRemoteCommandsDelivered(deviceID string, commandIDs []int64, now time.Time) error
	UpdateRemoteCommandResult(deviceID string, commandID int64, status, resultText, errorText string, resultJSON *RemoteCommandResult, expiresAt time.Time, now time.Time) error
	RecordEndpointErrorTelemetry(deviceID string, activationID, ownerAccountID int64, platform, appVersion string, items []EndpointErrorTelemetryItem, now time.Time) (int, error)
	ListEndpointErrorTelemetry(query EndpointErrorTelemetryQuery) ([]EndpointErrorTelemetryRecord, error)
	SummarizeEndpointErrorTelemetry(query EndpointErrorTelemetryQuery) ([]EndpointErrorTelemetrySummary, error)
}

type Service struct {
	store                     Store
	privateKey                ed25519.PrivateKey
	publicKey                 ed25519.PublicKey
	now                       func() time.Time
	remoteSecretRevealEnabled bool
	aiProvider                AIProvider
}

type ticketPayload struct {
	Product      string    `json:"product"`
	LicenseID    int64     `json:"licenseId"`
	ActivationID int64     `json:"activationId"`
	DeviceID     string    `json:"deviceId"`
	Plan         Plan      `json:"plan"`
	ExpiresAt    time.Time `json:"expiresAt"`
	NextCheckAt  time.Time `json:"nextCheckAt"`
	GraceUntil   time.Time `json:"graceUntil"`
	IssuedAt     time.Time `json:"issuedAt"`
}

type ticketEnvelope struct {
	Payload   ticketPayload `json:"payload"`
	Signature string        `json:"signature"`
}

func NewService(store Store, privateKey ed25519.PrivateKey, opts Options) *Service {
	service := &Service{
		store:                     store,
		privateKey:                privateKey,
		now:                       time.Now,
		remoteSecretRevealEnabled: opts.RemoteSecretRevealEnabled,
		aiProvider:                opts.AIProvider,
	}
	if opts.Now != nil {
		service.now = opts.Now
	}
	if privateKey != nil {
		if publicKey, ok := privateKey.Public().(ed25519.PublicKey); ok {
			service.publicKey = publicKey
		}
	}
	return service
}

func NewVerifier(publicKey ed25519.PublicKey, opts Options) *Service {
	service := &Service{publicKey: publicKey, now: time.Now}
	if opts.Now != nil {
		service.now = opts.Now
	}
	return service
}

func (s *Service) EnsureBootstrapAdmin(username, password string) (*AdminAccount, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("admin username is required")
	}
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("admin password is required")
	}
	now := s.currentTime()
	passwordHash, err := hashAdminPassword(password)
	if err != nil {
		return nil, err
	}
	account := &AdminAccount{
		Username:    username,
		DisplayName: "AINexus Admin",
		Level:       AdminLevelRoot,
		Status:      AdminAccountStatusActive,
		Permissions: defaultPermissionsForLevel(AdminLevelRoot),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if existing, _, err := s.store.GetAdminAccountByUsername(username); err == nil {
		account.ID = existing.ID
		account.DisplayName = existing.DisplayName
		account.CreatedAt = existing.CreatedAt
		account.CreatedBy = existing.CreatedBy
	}
	if err := s.store.UpsertAdminAccount(account, passwordHash); err != nil {
		return nil, err
	}
	if err := s.store.ClaimUnownedCards(account.ID); err != nil {
		return nil, err
	}
	return account, nil
}

func (s *Service) AuthenticateAdmin(username, password string) (*AdminAccount, error) {
	account, passwordHash, err := s.store.GetAdminAccountByUsername(strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if account.Status != AdminAccountStatusActive {
		return nil, ErrForbidden
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, ErrForbidden
	}
	return account, nil
}

func (s *Service) GetAdminAccount(id int64) (*AdminAccount, error) {
	account, _, err := s.store.GetAdminAccount(id)
	return account, err
}

func (s *Service) ListAdminAccountsFor(actor *AdminAccount) ([]AdminAccount, error) {
	if actor == nil || !hasPermission(actor, PermissionAccountsView) {
		return nil, ErrForbidden
	}
	accounts, err := s.store.ListAdminAccounts()
	if err != nil {
		return nil, err
	}
	if actor.Level == AdminLevelRoot {
		return accounts, nil
	}
	scope := accountScopeMap(actor, accounts)
	filtered := make([]AdminAccount, 0)
	for _, account := range accounts {
		if scope[account.ID] {
			filtered = append(filtered, account)
		}
	}
	return filtered, nil
}

func (s *Service) CreateAdminAccount(actor *AdminAccount, req CreateAdminAccountRequest) (*AdminAccount, error) {
	if actor == nil || !hasPermission(actor, PermissionAccountsManage) {
		return nil, ErrForbidden
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || strings.TrimSpace(req.Password) == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	level := req.Level
	if level == 0 {
		level = AdminLevelDistributor
	}
	if level < AdminLevelRoot || level > AdminLevelDistributor {
		return nil, fmt.Errorf("unsupported admin level")
	}
	if actor.Level != AdminLevelRoot && level <= actor.Level {
		return nil, ErrForbidden
	}
	parentID := req.ParentID
	if level == AdminLevelRoot {
		if actor.Level != AdminLevelRoot {
			return nil, ErrForbidden
		}
		parentID = 0
	} else if parentID == 0 {
		parentID = actor.ID
	}
	if err := s.validateAdminParent(actor, 0, level, parentID); err != nil {
		return nil, err
	}
	permissions := req.Permissions
	if len(permissions) == 0 {
		permissions = defaultPermissionsForLevel(level)
	}
	permissions = permissionsForActor(actor, permissions)
	now := s.currentTime()
	passwordHash, err := hashAdminPassword(req.Password)
	if err != nil {
		return nil, err
	}
	account := &AdminAccount{
		Username:    username,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Level:       level,
		ParentID:    parentID,
		Status:      AdminAccountStatusActive,
		Permissions: permissions,
		CreatedBy:   actor.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.UpsertAdminAccount(account, passwordHash); err != nil {
		return nil, err
	}
	_ = s.store.AddAudit("create_admin_account", "admin_account", account.ID, fmt.Sprintf("username=%s level=%d parent=%d", account.Username, account.Level, account.ParentID), now)
	return account, nil
}

func (s *Service) UpdateAdminAccount(actor *AdminAccount, id int64, req UpdateAdminAccountRequest) (*AdminAccount, error) {
	if actor == nil || !hasPermission(actor, PermissionAccountsManage) {
		return nil, ErrForbidden
	}
	account, _, err := s.store.GetAdminAccount(id)
	if err != nil {
		return nil, err
	}
	if actor.Level != AdminLevelRoot {
		if ok, err := s.accountInScope(actor, id); err != nil || !ok || actor.ID == id {
			if err != nil {
				return nil, err
			}
			return nil, ErrForbidden
		}
		if account.Level <= actor.Level {
			return nil, ErrForbidden
		}
	}
	if actor.ID == id && selfPrivilegeUpdateRequested(req) {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(req.DisplayName) != "" {
		account.DisplayName = strings.TrimSpace(req.DisplayName)
	}
	level := account.Level
	if req.hasLevel || req.Level != 0 {
		level = req.Level
	}
	if level < AdminLevelRoot || level > AdminLevelDistributor {
		return nil, fmt.Errorf("unsupported admin level")
	}
	if actor.Level != AdminLevelRoot && level <= actor.Level {
		return nil, ErrForbidden
	}
	parentID := account.ParentID
	if req.hasParentID || req.ParentID != 0 {
		parentID = req.ParentID
	}
	if level == AdminLevelRoot {
		if actor.Level != AdminLevelRoot {
			return nil, ErrForbidden
		}
		parentID = 0
	}
	if err := s.validateAdminParent(actor, account.ID, level, parentID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Status) != "" {
		status := strings.TrimSpace(req.Status)
		if status != AdminAccountStatusActive && status != AdminAccountStatusDisabled {
			return nil, fmt.Errorf("unsupported admin status")
		}
		account.Status = status
	}
	if len(req.Permissions) > 0 {
		account.Permissions = permissionsForActor(actor, req.Permissions)
	}
	account.Level = level
	account.ParentID = parentID
	account.UpdatedAt = s.currentTime()
	passwordHash := ""
	if strings.TrimSpace(req.Password) != "" {
		passwordHash, err = hashAdminPassword(req.Password)
		if err != nil {
			return nil, err
		}
	}
	if err := s.store.UpdateAdminAccount(account, passwordHash); err != nil {
		return nil, err
	}
	_ = s.store.AddAudit("update_admin_account", "admin_account", account.ID, fmt.Sprintf("username=%s status=%s level=%d parent=%d", account.Username, account.Status, account.Level, account.ParentID), account.UpdatedAt)
	return account, nil
}

func (s *Service) validateAdminParent(actor *AdminAccount, targetID int64, level int, parentID int64) error {
	if actor == nil {
		return ErrForbidden
	}
	if level == AdminLevelRoot {
		if parentID != 0 || actor.Level != AdminLevelRoot {
			return ErrForbidden
		}
		return nil
	}
	if parentID == 0 || parentID == targetID {
		return fmt.Errorf("invalid admin parent")
	}
	accounts, err := s.store.ListAdminAccounts()
	if err != nil {
		return err
	}
	var parent *AdminAccount
	var target *AdminAccount
	for i := range accounts {
		if accounts[i].ID == parentID {
			parent = &accounts[i]
		}
		if accounts[i].ID == targetID {
			target = &accounts[i]
		}
	}
	if parent == nil || parent.Level >= level {
		return fmt.Errorf("invalid admin parent hierarchy")
	}
	if actor.Level != AdminLevelRoot && !accountScopeMap(actor, accounts)[parentID] {
		return ErrForbidden
	}
	if target != nil && accountScopeMap(target, accounts)[parentID] {
		return fmt.Errorf("admin hierarchy cycle")
	}
	return nil
}

func selfPrivilegeUpdateRequested(req UpdateAdminAccountRequest) bool {
	return req.hasLevel ||
		req.hasParentID ||
		req.hasStatus ||
		req.hasPermissions
}

func (s *Service) GenerateCards(req GenerateCardsRequest) (*GenerateCardsResult, error) {
	count := req.Count
	if count <= 0 {
		return nil, fmt.Errorf("count must be positive")
	}
	days, err := ResolveDurationDays(req.Plan, req.Days)
	if err != nil {
		return nil, err
	}
	maxDevices := req.MaxDevices
	if maxDevices <= 0 {
		maxDevices = 1
	}
	now := s.currentTime()
	result := &GenerateCardsResult{Cards: make([]GeneratedCard, 0, count)}
	for i := 0; i < count; i++ {
		cardKey, err := randomCardKey()
		if err != nil {
			return nil, err
		}
		card := &CardRecord{
			CardHash:           HashCardKey(cardKey),
			Plan:               req.Plan,
			Days:               days,
			MaxDevices:         maxDevices,
			Status:             CardStatusActive,
			Customer:           strings.TrimSpace(req.Customer),
			Remark:             strings.TrimSpace(req.Remark),
			OwnerAccountID:     req.OwnerAccountID,
			CreatedByAccountID: req.CreatedByAccountID,
			CreatedAt:          now,
		}
		if err := s.store.CreateCard(card); err != nil {
			return nil, err
		}
		detail := fmt.Sprintf("plan=%s days=%d maxDevices=%d customer=%s remark=%s", card.Plan, card.Days, card.MaxDevices, card.Customer, card.Remark)
		if err := s.store.AddAudit("generate_card", "card", card.ID, detail, now); err != nil {
			return nil, err
		}
		result.Cards = append(result.Cards, GeneratedCard{
			ID:                 card.ID,
			CardKey:            cardKey,
			CardHash:           card.CardHash,
			Plan:               card.Plan,
			Days:               card.Days,
			MaxDevices:         card.MaxDevices,
			Customer:           card.Customer,
			Remark:             card.Remark,
			Status:             card.Status,
			OwnerAccountID:     card.OwnerAccountID,
			CreatedByAccountID: card.CreatedByAccountID,
			CreatedAt:          card.CreatedAt,
		})
	}
	csvText, err := cardsCSV(result.Cards)
	if err != nil {
		return nil, err
	}
	result.CSV = csvText
	return result, nil
}

func (s *Service) Activate(req ActivationRequest) (*ActivationResult, error) {
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("device id is required")
	}
	now := s.currentTime()
	activation, err := s.store.ActivateCard(
		HashCardKey(req.CardKey),
		deviceID,
		now,
		strings.TrimSpace(req.Platform),
		strings.TrimSpace(req.AppVersion),
		strings.TrimSpace(req.IPAddress),
	)
	if err != nil {
		return nil, err
	}
	if activation.ActivatedAt.Equal(now) {
		_ = s.store.AddAudit("activate", "activation", activation.ID, deviceID, now)
	}
	_ = s.upsertRemoteStateFromActivation(activation, req.Remote, strings.TrimSpace(req.AppVersion), now)
	return s.resultFor(activation, now, "license is active")
}

func (s *Service) Refresh(req RefreshRequest) (*ActivationResult, error) {
	payload, err := s.decodeAndVerifyTicket(req.Ticket)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.DeviceID) != "" && payload.DeviceID != strings.TrimSpace(req.DeviceID) {
		return nil, ErrInvalidTicket
	}
	activation, err := s.store.GetActivation(payload.ActivationID)
	if err != nil {
		return nil, err
	}
	card, cardErr := s.store.GetCard(activation.CardID)
	if activation.Status != ActivationStatusActive || cardErr != nil || card.Status != CardStatusActive {
		fallback, fallbackErr := s.store.LatestActivationForDevice(payload.DeviceID)
		if fallbackErr != nil {
			if activation.Status != ActivationStatusActive {
				return nil, ErrActivationBlocked
			}
			return nil, ErrCardDisabled
		}
		fallbackCard, fallbackCardErr := s.store.GetCard(fallback.CardID)
		if fallbackCardErr != nil || fallbackCard.Status != CardStatusActive {
			return nil, ErrCardDisabled
		}
		activation = fallback
	}
	now := s.currentTime()
	if err := s.store.TouchActivation(activation.ID, now, strings.TrimSpace(req.Platform), strings.TrimSpace(req.AppVersion), strings.TrimSpace(req.IPAddress)); err != nil {
		return nil, err
	}
	activation.LastCheckedAt = now
	if strings.TrimSpace(req.AppVersion) != "" {
		activation.AppVersion = strings.TrimSpace(req.AppVersion)
	}
	_ = s.upsertRemoteStateFromActivation(activation, req.Remote, strings.TrimSpace(req.AppVersion), now)
	return s.resultFor(activation, now, "license refreshed")
}

func (s *Service) VerifyTicket(ticket, deviceID string, now time.Time) (*TicketStatus, error) {
	payload, err := s.decodeAndVerifyTicket(ticket)
	if err != nil {
		return nil, err
	}
	if payload.Product != ProductCCNexusPro {
		return nil, ErrInvalidTicket
	}
	if strings.TrimSpace(deviceID) != "" && payload.DeviceID != strings.TrimSpace(deviceID) {
		return nil, ErrInvalidTicket
	}
	now = now.UTC()
	if now.IsZero() {
		now = s.currentTime()
	}
	if now.After(payload.GraceUntil) {
		return nil, ErrTicketExpired
	}
	return &TicketStatus{
		Licensed:     now.Before(payload.ExpiresAt) || now.Equal(payload.ExpiresAt),
		LicenseID:    payload.LicenseID,
		ActivationID: payload.ActivationID,
		DeviceID:     payload.DeviceID,
		Plan:         payload.Plan,
		ExpiresAt:    payload.ExpiresAt,
		NextCheckAt:  payload.NextCheckAt,
		GraceUntil:   payload.GraceUntil,
		Message:      "license ticket is valid",
	}, nil
}

func (s *Service) ListCards() ([]CardRecord, error) {
	return s.store.ListCards()
}

func (s *Service) ListCardsFor(actor *AdminAccount) ([]CardRecord, error) {
	if actor == nil || !hasPermission(actor, PermissionCardsView) {
		return nil, ErrForbidden
	}
	if actor.Level == AdminLevelRoot {
		return s.store.ListCards()
	}
	ownerIDs, err := s.ownerScopeIDs(actor)
	if err != nil {
		return nil, err
	}
	return s.store.ListCardsByOwner(ownerIDs)
}

func (s *Service) ListActivations() ([]ActivationRecord, error) {
	return s.store.ListActivations()
}

func (s *Service) ListActivationsFor(actor *AdminAccount) ([]ActivationRecord, error) {
	if actor == nil || !hasPermission(actor, PermissionActivationsView) {
		return nil, ErrForbidden
	}
	if actor.Level == AdminLevelRoot {
		return s.store.ListActivations()
	}
	ownerIDs, err := s.ownerScopeIDs(actor)
	if err != nil {
		return nil, err
	}
	return s.store.ListActivationsByOwner(ownerIDs)
}

func (s *Service) ListDevices() ([]DeviceRecord, error) {
	activations, err := s.store.ListActivations()
	if err != nil {
		return nil, err
	}
	return s.devicesFromActivations(activations)
}

func (s *Service) ListDevicesFor(actor *AdminAccount) ([]DeviceRecord, error) {
	if actor == nil || !hasPermission(actor, PermissionDevicesView) {
		return nil, ErrForbidden
	}
	if actor.Level == AdminLevelRoot {
		return s.ListDevices()
	}
	ownerIDs, err := s.ownerScopeIDs(actor)
	if err != nil {
		return nil, err
	}
	activations, err := s.store.ListActivationsByOwner(ownerIDs)
	if err != nil {
		return nil, err
	}
	return s.devicesFromActivations(activations)
}

func (s *Service) devicesFromActivations(activations []ActivationRecord) ([]DeviceRecord, error) {
	remarks, err := s.store.ListDeviceRemarks()
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	devices := make([]DeviceRecord, 0)
	index := make(map[string]int)
	for _, activation := range activations {
		position, ok := index[activation.DeviceID]
		if !ok {
			position = len(devices)
			index[activation.DeviceID] = position
			devices = append(devices, DeviceRecord{
				DeviceID: activation.DeviceID,
				Status:   ActivationStatusDisabled,
				Licenses: make([]ActivationRecord, 0, 1),
			})
		}
		device := &devices[position]
		device.Licenses = append(device.Licenses, activation)
		if device.OwnerAccountID == 0 {
			device.OwnerAccountID = activation.OwnerAccountID
			device.OwnerUsername = activation.OwnerUsername
		}
		if device.LastCheckedAt.IsZero() || activation.LastCheckedAt.After(device.LastCheckedAt) {
			device.LastCheckedAt = activation.LastCheckedAt
			device.Platform = activation.Platform
			device.AppVersion = activation.AppVersion
			device.IPAddress = activation.IPAddress
		}
		if activation.Status == ActivationStatusActive && (device.CurrentActivationID == 0 || activation.ExpiresAt.After(device.ExpiresAt)) {
			device.CurrentActivationID = activation.ID
			device.ExpiresAt = activation.ExpiresAt
		}
	}
	for i := range devices {
		device := &devices[i]
		device.Remark = remarks[device.DeviceID]
		if device.CurrentActivationID == 0 {
			if len(device.Licenses) > 0 {
				device.CurrentActivationID = device.Licenses[0].ID
				device.ExpiresAt = device.Licenses[0].ExpiresAt
			}
			continue
		}
		if device.ExpiresAt.After(now) {
			device.Status = ActivationStatusActive
		} else {
			device.Status = "expired"
		}
	}
	return devices, nil
}

func (s *Service) GenerateCardsFor(actor *AdminAccount, req GenerateCardsRequest) (*GenerateCardsResult, error) {
	if actor == nil || !hasPermission(actor, PermissionCardsGenerate) {
		return nil, ErrForbidden
	}
	ownerID := req.OwnerAccountID
	if ownerID == 0 {
		ownerID = actor.ID
	}
	if ok, err := s.accountInScope(actor, ownerID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, ErrForbidden
	}
	req.OwnerAccountID = ownerID
	req.CreatedByAccountID = actor.ID
	return s.GenerateCards(req)
}

func (s *Service) ListAudit() ([]AuditRecord, error) {
	return s.store.ListAudit(200)
}

func (s *Service) DisableCard(id int64) error {
	now := s.currentTime()
	card, err := s.store.GetCard(id)
	if err != nil {
		return err
	}
	if err := s.store.DisableCard(id, now); err != nil {
		return err
	}
	detail := fmt.Sprintf("plan=%s days=%d disabledAt=%s", card.Plan, card.Days, now.Format(time.RFC3339))
	return s.store.AddAudit("disable_card", "card", id, detail, now)
}

func (s *Service) DisableCardFor(actor *AdminAccount, id int64) error {
	if actor == nil || !hasPermission(actor, PermissionCardsDisable) {
		return ErrForbidden
	}
	card, err := s.store.GetCard(id)
	if err != nil {
		return err
	}
	if ok, err := s.accountInScope(actor, card.OwnerAccountID); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.DisableCard(id)
}

func (s *Service) DeleteCard(id int64) error {
	now := s.currentTime()
	card, err := s.store.GetCard(id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteCard(id); err != nil {
		if errors.Is(err, ErrInvalidCard) {
			return err
		}
		return err
	}
	detail := fmt.Sprintf("plan=%s days=%d customer=%s remark=%s", card.Plan, card.Days, card.Customer, card.Remark)
	return s.store.AddAudit("delete_card", "card", id, detail, now)
}

func (s *Service) DeleteCardFor(actor *AdminAccount, id int64) error {
	if actor == nil || !hasPermission(actor, PermissionCardsDelete) {
		return ErrForbidden
	}
	card, err := s.store.GetCard(id)
	if err != nil {
		return err
	}
	if ok, err := s.accountInScope(actor, card.OwnerAccountID); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.DeleteCard(id)
}

func (s *Service) DisableActivation(id int64) error {
	now := s.currentTime()
	activation, err := s.store.GetActivation(id)
	if err != nil {
		return err
	}
	if err := s.store.DisableActivation(id, now); err != nil {
		return err
	}
	detail := fmt.Sprintf("device=%s oldExpiry=%s newExpiry=%s", activation.DeviceID, activation.ExpiresAt.Format(time.RFC3339), now.Format(time.RFC3339))
	return s.store.AddAudit("disable_activation", "activation", id, detail, now)
}

func (s *Service) DisableActivationFor(actor *AdminAccount, id int64) error {
	if actor == nil || !hasPermission(actor, PermissionActivationsDisable) {
		return ErrForbidden
	}
	activation, err := s.store.GetActivation(id)
	if err != nil {
		return err
	}
	if ok, err := s.accountInScope(actor, activation.OwnerAccountID); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.DisableActivation(id)
}

func (s *Service) SetDeviceExpiry(deviceID string, expiresAt time.Time) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" || expiresAt.IsZero() {
		return fmt.Errorf("device id and expiry are required")
	}
	devices, err := s.ListDevices()
	if err != nil {
		return err
	}
	var current *DeviceRecord
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			current = &devices[i]
			break
		}
	}
	if current == nil {
		return sql.ErrNoRows
	}
	now := s.currentTime()
	expiresAt = expiresAt.UTC()
	if err := s.store.SetDeviceExpiry(deviceID, expiresAt, now); err != nil {
		return err
	}
	detail := fmt.Sprintf("device=%s oldExpiry=%s newExpiry=%s", deviceID, current.ExpiresAt.Format(time.RFC3339), expiresAt.Format(time.RFC3339))
	return s.store.AddAudit("set_device_expiry", "activation", current.CurrentActivationID, detail, now)
}

func (s *Service) SetDeviceExpiryFor(actor *AdminAccount, deviceID string, expiresAt time.Time) error {
	if actor == nil || !hasPermission(actor, PermissionDevicesExpiry) {
		return ErrForbidden
	}
	if ok, err := s.deviceInScope(actor, deviceID); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.SetDeviceExpiry(deviceID, expiresAt)
}

func (s *Service) SetDeviceRemark(deviceID, remark string) error {
	deviceID = strings.TrimSpace(deviceID)
	remark = strings.TrimSpace(remark)
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	if len([]rune(remark)) > 500 {
		return fmt.Errorf("device remark is too long")
	}
	devices, err := s.ListDevices()
	if err != nil {
		return err
	}
	var current *DeviceRecord
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			current = &devices[i]
			break
		}
	}
	if current == nil {
		return sql.ErrNoRows
	}
	now := s.currentTime()
	if err := s.store.SetDeviceRemark(deviceID, remark, now); err != nil {
		return err
	}
	detail := fmt.Sprintf("device=%s oldRemark=%s newRemark=%s", deviceID, current.Remark, remark)
	return s.store.AddAudit("set_device_remark", "device", 0, detail, now)
}

func (s *Service) SetDeviceRemarkFor(actor *AdminAccount, deviceID, remark string) error {
	if actor == nil || !hasPermission(actor, PermissionDevicesRemark) {
		return ErrForbidden
	}
	if ok, err := s.deviceInScope(actor, deviceID); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	return s.SetDeviceRemark(deviceID, remark)
}

func (s *Service) RemoteDeviceDetailFor(actor *AdminAccount, deviceID string) (*RemoteAdminDetail, error) {
	if actor == nil || !hasPermission(actor, PermissionDevicesRemoteView) {
		return nil, ErrForbidden
	}
	if ok, err := s.deviceInScope(actor, deviceID); err != nil || !ok {
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrForbidden
			}
			return nil, err
		}
		return nil, ErrForbidden
	}
	state, err := s.store.GetRemoteDevice(strings.TrimSpace(deviceID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &RemoteAdminDetail{State: RemoteDeviceState{DeviceID: strings.TrimSpace(deviceID), Supported: false}}, nil
		}
		return nil, err
	}
	commands, err := s.store.ListRemoteCommands(strings.TrimSpace(deviceID), 20)
	if err != nil {
		return nil, err
	}
	commands = redactRemoteCommandsForActor(actor, commands)
	return &RemoteAdminDetail{State: *state, Commands: commands}, nil
}

func redactRemoteCommandsForActor(actor *AdminAccount, commands []RemoteCommandRecord) []RemoteCommandRecord {
	canViewSecrets := hasPermission(actor, PermissionDevicesRemoteSecrets)
	for i := range commands {
		commands[i].Envelope = RemoteEnvelope{}
		if commands[i].CommandType == "secret.reveal" && !canViewSecrets {
			commands[i].ResultJSON = nil
			commands[i].Result = ""
		}
	}
	return commands
}

func (s *Service) QueueRemoteCommandFor(actor *AdminAccount, deviceID string, req RemoteCommandRequest) (*RemoteCommandRecord, error) {
	if actor == nil || !hasPermission(actor, PermissionDevicesRemoteWrite) {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(req.CommandType) == "secret.reveal" {
		return nil, ErrForbidden
	}
	return s.queueRemoteCommandFor(actor, deviceID, req)
}

func (s *Service) queueRemoteCommandFor(actor *AdminAccount, deviceID string, req RemoteCommandRequest) (*RemoteCommandRecord, error) {
	device, err := s.remoteDeviceForWrite(actor, deviceID)
	if err != nil {
		return nil, err
	}
	commandType := strings.TrimSpace(req.CommandType)
	if commandType == "" {
		return nil, fmt.Errorf("command type is required")
	}
	payload := map[string]interface{}{
		"commandType": commandType,
		"payload":     req.Payload,
		"queuedAt":    s.currentTime().UTC().Format(time.RFC3339Nano),
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope, err := EncryptRemoteEnvelope(device.DevicePublicKey, plain)
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	expiresAt := req.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.UTC().Add(remoteCommandTTL)
	}
	command := &RemoteCommandRecord{
		DeviceID:    strings.TrimSpace(deviceID),
		CommandType: commandType,
		Status:      RemoteCommandStatusQueued,
		ActorID:     actor.ID,
		ActorName:   actor.Username,
		Summary:     buildRemoteCommandSummary(commandType, req.Payload),
		Envelope:    envelope,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := signRemoteCommand(s.privateKey, command); err != nil {
		return nil, err
	}
	if err := s.store.CreateRemoteCommand(command, device.OwnerAccountID); err != nil {
		return nil, err
	}
	_ = s.store.AddAudit("remote_command", "device", 0, fmt.Sprintf("device=%s command=%s actor=%s", command.DeviceID, command.CommandType, actor.Username), now)
	return command, nil
}

func buildRemoteCommandSummary(commandType string, payload interface{}) *RemoteCommandSummary {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var values map[string]interface{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil
	}
	summary := &RemoteCommandSummary{RiskLevel: "normal"}
	switch strings.TrimSpace(commandType) {
	case "endpoint.create":
		summary.TargetType = "endpoint"
		summary.TargetName = remoteSummaryString(values["name"])
		summary.ChangedFields = remoteSummaryFields(values, "name")
	case "endpoint.update":
		summary.TargetType = "endpoint"
		summary.TargetName = remoteSummaryString(values["endpointName"])
		summary.ChangedFields = remoteSummaryFields(values, "endpointName")
	case "endpoint.delete":
		summary.TargetType = "endpoint"
		summary.TargetName = remoteSummaryString(values["endpointName"])
		summary.RiskLevel = "destructive"
	case "endpoint.reorder":
		summary.TargetType = "endpoint"
		summary.ChangedFields = []string{"order"}
	case "credential.setEnabled":
		summary.TargetType = "credential"
		summary.CredentialID = remoteSummaryInt64(values["credentialId"])
		summary.ChangedFields = []string{"enabled"}
	case "credential.updateToken":
		summary.TargetType = "credential"
		summary.CredentialID = remoteSummaryInt64(values["credentialId"])
		summary.ChangedFields = []string{"accessToken"}
		summary.RiskLevel = "sensitive"
	case "credential.delete":
		summary.TargetType = "credential"
		summary.CredentialID = remoteSummaryInt64(values["credentialId"])
		summary.RiskLevel = "destructive"
	case "secret.reveal":
		summary.TargetType = "endpoint"
		summary.TargetName = remoteSummaryString(values["endpointName"])
		summary.CredentialID = remoteSummaryInt64(values["credentialId"])
		if summary.CredentialID > 0 {
			summary.TargetType = "credential"
		}
		summary.ChangedFields = []string{"secret"}
		summary.RiskLevel = "sensitive"
	default:
		return nil
	}
	for _, field := range summary.ChangedFields {
		if field == "apiKey" || field == "authMode" || field == "accessToken" {
			summary.RiskLevel = "sensitive"
			break
		}
	}
	return summary
}

func remoteSummaryFields(values map[string]interface{}, excluded string) []string {
	fields := make([]string, 0, len(values))
	for key := range values {
		if key == excluded {
			continue
		}
		switch key {
		case "name", "apiUrl", "apiKey", "authMode", "transformer", "model", "thinking",
			"codexFastMode", "maxConcurrentRequests", "enabled":
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}

func remoteSummaryString(value interface{}) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func remoteSummaryInt64(value interface{}) int64 {
	number, _ := value.(float64)
	return int64(number)
}

func (s *Service) QueueRemoteSecretRevealFor(actor *AdminAccount, deviceID string, req RemoteSecretRevealRequest) (*RemoteCommandRecord, error) {
	if actor == nil || !hasPermission(actor, PermissionDevicesRemoteSecrets) {
		return nil, ErrForbidden
	}
	if !s.remoteSecretRevealEnabled {
		return nil, ErrForbidden
	}
	adminPublicKey := strings.TrimSpace(req.AdminPublicKey)
	if adminPublicKey == "" {
		return nil, fmt.Errorf("admin public key is required")
	}
	if _, err := DecodeRemoteRevealPublicKey(adminPublicKey); err != nil {
		return nil, fmt.Errorf("invalid admin public key: %w", err)
	}
	expiresAt := s.currentTime().UTC().Add(remoteSecretRevealTTL)
	payload := RemoteSecretRevealPayload{
		EndpointName:   strings.TrimSpace(req.EndpointName),
		CredentialID:   req.CredentialID,
		Field:          strings.TrimSpace(req.Field),
		AdminPublicKey: adminPublicKey,
		ExpiresAt:      expiresAt.Format(time.RFC3339Nano),
	}
	command, err := s.queueRemoteCommandFor(actor, deviceID, RemoteCommandRequest{
		CommandType: "secret.reveal",
		Payload:     payload,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}
	_ = s.store.AddAudit("remote_secret_reveal", "device", 0, fmt.Sprintf("device=%s endpoint=%s credentialId=%d field=%s actor=%s", strings.TrimSpace(deviceID), strings.TrimSpace(req.EndpointName), req.CredentialID, strings.TrimSpace(req.Field), actor.Username), s.currentTime())
	return command, nil
}

func (s *Service) RemoteCommandFor(actor *AdminAccount, deviceID string, commandID int64) (*RemoteCommandRecord, error) {
	if actor == nil || (!hasPermission(actor, PermissionDevicesRemoteView) && !hasPermission(actor, PermissionDevicesRemoteWrite) && !hasPermission(actor, PermissionDevicesRemoteSecrets)) {
		return nil, ErrForbidden
	}
	if ok, err := s.deviceInScope(actor, deviceID); err != nil || !ok {
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrForbidden
			}
			return nil, err
		}
		return nil, ErrForbidden
	}
	command, err := s.store.GetRemoteCommand(strings.TrimSpace(deviceID), commandID)
	if err != nil {
		return nil, err
	}
	if command.CommandType == "secret.reveal" && !hasPermission(actor, PermissionDevicesRemoteSecrets) {
		return nil, ErrForbidden
	}
	if command.CommandType != "secret.reveal" && !hasPermission(actor, PermissionDevicesRemoteView) && !hasPermission(actor, PermissionDevicesRemoteWrite) {
		return nil, ErrForbidden
	}
	if !command.ExpiresAt.IsZero() && !s.currentTime().Before(command.ExpiresAt) &&
		(command.Status == RemoteCommandStatusQueued || command.Status == RemoteCommandStatusDelivered) {
		now := s.currentTime()
		_ = s.store.UpdateRemoteCommandResult(strings.TrimSpace(deviceID), command.ID, RemoteCommandStatusExpired, "", "remote command expired", nil, time.Time{}, now)
		command.Status = RemoteCommandStatusExpired
		command.Error = "remote command expired"
		command.UpdatedAt = now.UTC()
		return command, nil
	}
	if command.ResultJSON != nil && command.ResultJSON.SecretReveal != nil {
		expiresAt := command.ResultJSON.SecretReveal.ExpiresAt
		if !expiresAt.IsZero() && !s.currentTime().Before(expiresAt) {
			return nil, ErrRemoteResultExpired
		}
	}
	return command, nil
}

func (s *Service) PollRemoteCommands(req RemotePollRequest) (*RemotePollResponse, error) {
	if err := s.verifyRemoteTicketDevice(req.Ticket, req.DeviceID); err != nil {
		return nil, err
	}
	commands, err := s.store.ListQueuedRemoteCommands(strings.TrimSpace(req.DeviceID), 10)
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	deliverable := make([]RemoteCommandRecord, 0, len(commands))
	deliveredIDs := make([]int64, 0, len(commands))
	for _, command := range commands {
		if !command.ExpiresAt.IsZero() && !now.Before(command.ExpiresAt) {
			_ = s.store.UpdateRemoteCommandResult(strings.TrimSpace(req.DeviceID), command.ID, RemoteCommandStatusExpired, "", "remote command expired", nil, time.Time{}, now)
			continue
		}
		if strings.TrimSpace(command.CommandNonce) == "" || strings.TrimSpace(command.Signature) == "" {
			if err := signRemoteCommand(s.privateKey, &command); err != nil {
				_ = s.store.UpdateRemoteCommandResult(strings.TrimSpace(req.DeviceID), command.ID, RemoteCommandStatusFailed, "", "legacy remote command could not be signed", nil, time.Time{}, now)
				continue
			}
			if err := s.store.UpdateRemoteCommandSignature(command.DeviceID, command.ID, command.CommandNonce, command.Signature, now); err != nil {
				return nil, err
			}
		}
		command.Status = RemoteCommandStatusDelivered
		command.UpdatedAt = now.UTC()
		deliverable = append(deliverable, command)
		deliveredIDs = append(deliveredIDs, command.ID)
	}
	if err := s.store.MarkRemoteCommandsDelivered(strings.TrimSpace(req.DeviceID), deliveredIDs, now); err != nil {
		return nil, err
	}
	return &RemotePollResponse{Commands: deliverable}, nil
}

func (s *Service) SubmitRemoteResult(req RemoteResultRequest) error {
	if err := s.verifyRemoteTicketDevice(req.Ticket, req.DeviceID); err != nil {
		return err
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = RemoteCommandStatusApplied
	}
	now := s.currentTime()
	resultText := ""
	var resultJSON *RemoteCommandResult
	if req.Result != nil {
		copyResult := *req.Result
		resultJSON = &copyResult
	}
	if req.Snapshot != nil {
		if err := s.store.UpsertRemoteSnapshot(strings.TrimSpace(req.DeviceID), *req.Snapshot, now); err != nil {
			return err
		}
		if resultJSON == nil {
			resultJSON = &RemoteCommandResult{}
		}
		resultJSON.SnapshotUpdated = true
		resultJSON.SnapshotUpdatedAt = now.UTC()
		resultText = "snapshot updated"
	}
	expiresAt := time.Time{}
	if req.SecretReveal != nil {
		if resultJSON == nil {
			resultJSON = &RemoteCommandResult{}
		}
		resultJSON.SecretRevealReady = true
		resultJSON.SecretReveal = req.SecretReveal
		expiresAt = req.SecretReveal.ExpiresAt
		resultText = "secret reveal encrypted"
	}
	if resultJSON != nil && resultJSON.Message == "" && resultText != "" {
		resultJSON.Message = resultText
	}
	if req.CommandID == 0 {
		return nil
	}
	return s.store.UpdateRemoteCommandResult(strings.TrimSpace(req.DeviceID), req.CommandID, status, resultText, strings.TrimSpace(req.Error), resultJSON, expiresAt, now)
}

func (s *Service) SubmitEndpointErrorTelemetry(req EndpointErrorTelemetryRequest) (*EndpointErrorTelemetryResult, error) {
	deviceID := strings.TrimSpace(req.DeviceID)
	_, activation, err := s.validateOnlineTicket(req.Ticket, deviceID)
	if err != nil {
		return nil, err
	}
	items := make([]EndpointErrorTelemetryItem, 0, len(req.Items))
	for _, item := range req.Items {
		item = normalizeEndpointErrorTelemetryItem(item)
		item.Sample = sanitizeEndpointTelemetrySample(item.Sample)
		if item.EndpointFingerprint == "" || item.Reason == "" || item.Count <= 0 || item.WindowStart.IsZero() || item.WindowEnd.IsZero() {
			continue
		}
		items = append(items, item)
	}
	accepted, err := s.store.RecordEndpointErrorTelemetry(deviceID, activation.ID, activation.OwnerAccountID, strings.TrimSpace(req.Platform), strings.TrimSpace(req.AppVersion), items, s.currentTime())
	if err != nil {
		return nil, err
	}
	return &EndpointErrorTelemetryResult{Accepted: accepted}, nil
}

func (s *Service) EndpointErrorTelemetryFor(actor *AdminAccount, query EndpointErrorTelemetryQuery) (*EndpointErrorTelemetryResponse, error) {
	if actor == nil || !hasPermission(actor, PermissionDevicesRemoteView) {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(query.DeviceID) != "" {
		if ok, err := s.deviceInScope(actor, strings.TrimSpace(query.DeviceID)); err != nil || !ok {
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, ErrForbidden
				}
				return nil, err
			}
			return nil, ErrForbidden
		}
	} else if actor.Level != AdminLevelRoot {
		return nil, ErrForbidden
	}
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 200
	}
	items, err := s.store.ListEndpointErrorTelemetry(query)
	if err != nil {
		return nil, err
	}
	summary, err := s.store.SummarizeEndpointErrorTelemetry(query)
	if err != nil {
		return nil, err
	}
	response := &EndpointErrorTelemetryResponse{
		DeviceID: query.DeviceID,
		From:     query.From,
		To:       query.To,
		Items:    items,
		Summary:  summary,
	}
	if len(items) > 0 {
		response.Platform = items[0].Platform
		response.AppVersion = items[0].AppVersion
	}
	return response, nil
}

func sanitizeEndpointTelemetrySample(sample string) string {
	sample = strings.TrimSpace(sample)
	if sample == "" {
		return ""
	}
	sample = endpointTelemetrySecretPattern.ReplaceAllString(sample, "[redacted]")
	sample = endpointTelemetryURLQueryPattern.ReplaceAllString(sample, "$1?[redacted]")
	sample = endpointTelemetryKVSecretPattern.ReplaceAllString(sample, "$1=[redacted]")
	sample = strings.ReplaceAll(sample, "prompt text", "[redacted-prompt]")
	sample = strings.ReplaceAll(sample, "response text", "[redacted-response]")
	if len(sample) > 200 {
		sample = sample[:200]
	}
	return sample
}

func (s *Service) upsertRemoteStateFromActivation(activation *ActivationRecord, report RemoteCapabilityReport, appVersion string, now time.Time) error {
	if activation == nil || strings.TrimSpace(activation.DeviceID) == "" {
		return nil
	}
	if !report.Supported && strings.TrimSpace(report.PublicKey) == "" && len(report.Capabilities) == 0 {
		return nil
	}
	if strings.TrimSpace(report.PublicKey) != "" {
		if _, err := DecodeRemotePublicKey(strings.TrimSpace(report.PublicKey)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(appVersion) == "" {
		appVersion = activation.AppVersion
	}
	return s.store.UpsertRemoteDeviceState(&RemoteDeviceState{
		DeviceID:           activation.DeviceID,
		Supported:          report.Supported,
		Enabled:            report.Enabled,
		ClientVersion:      strings.TrimSpace(appVersion),
		Capabilities:       normalizeRemoteCapabilities(report.Capabilities),
		DevicePublicKey:    strings.TrimSpace(report.PublicKey),
		LastHeartbeatAt:    now,
		LastActivationID:   activation.ID,
		OwnerAccountID:     activation.OwnerAccountID,
		OwnerUsername:      activation.OwnerUsername,
		LastSnapshotStatus: "pending",
	}, now)
}

func (s *Service) remoteDeviceForWrite(actor *AdminAccount, deviceID string) (*RemoteDeviceState, error) {
	if ok, err := s.deviceInScope(actor, deviceID); err != nil || !ok {
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrForbidden
			}
			return nil, err
		}
		return nil, ErrForbidden
	}
	device, err := s.store.GetRemoteDevice(strings.TrimSpace(deviceID))
	if err != nil {
		return nil, err
	}
	if !device.Supported || !device.Enabled || strings.TrimSpace(device.DevicePublicKey) == "" {
		return nil, fmt.Errorf("device does not support remote management")
	}
	return device, nil
}

func (s *Service) verifyRemoteTicketDevice(ticket, deviceID string) error {
	_, _, err := s.validateOnlineTicket(ticket, strings.TrimSpace(deviceID))
	return err
}

func (s *Service) validateOnlineTicket(ticket, deviceID string) (*ticketPayload, *ActivationRecord, error) {
	payload, err := s.decodeAndVerifyTicket(ticket)
	if err != nil {
		return nil, nil, err
	}
	deviceID = strings.TrimSpace(deviceID)
	if payload.Product != ProductCCNexusPro || deviceID == "" || payload.DeviceID != deviceID {
		return nil, nil, ErrInvalidTicket
	}
	now := s.currentTime().UTC()
	if now.After(payload.GraceUntil.UTC()) || now.After(payload.ExpiresAt.UTC()) {
		return nil, nil, ErrTicketExpired
	}
	activation, err := s.store.GetActivation(payload.ActivationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrInvalidTicket
		}
		return nil, nil, err
	}
	if activation.ID != payload.ActivationID || activation.CardID != payload.LicenseID || activation.DeviceID != payload.DeviceID {
		return nil, nil, ErrInvalidTicket
	}
	if activation.Status != ActivationStatusActive {
		return nil, nil, ErrActivationBlocked
	}
	if now.After(activation.ExpiresAt.UTC()) {
		return nil, nil, ErrTicketExpired
	}
	card, err := s.store.GetCard(activation.CardID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrInvalidTicket
		}
		return nil, nil, err
	}
	if card.Status != CardStatusActive {
		return nil, nil, ErrCardDisabled
	}
	return payload, activation, nil
}

func (s *Service) RecordAudit(action, targetType string, targetID int64, detail string) error {
	return s.store.AddAudit(strings.TrimSpace(action), strings.TrimSpace(targetType), targetID, strings.TrimSpace(detail), s.currentTime())
}

func (s *Service) ownerScopeIDs(actor *AdminAccount) ([]int64, error) {
	if actor == nil {
		return nil, ErrForbidden
	}
	if actor.Level == AdminLevelRoot {
		accounts, err := s.store.ListAdminAccounts()
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(accounts))
		for _, account := range accounts {
			ids = append(ids, account.ID)
		}
		return ids, nil
	}
	accounts, err := s.store.ListAdminAccounts()
	if err != nil {
		return nil, err
	}
	scope := accountScopeMap(actor, accounts)
	ids := make([]int64, 0, len(scope))
	for id := range scope {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Service) accountInScope(actor *AdminAccount, accountID int64) (bool, error) {
	if actor == nil || accountID == 0 {
		return false, nil
	}
	if actor.Level == AdminLevelRoot {
		return true, nil
	}
	accounts, err := s.store.ListAdminAccounts()
	if err != nil {
		return false, err
	}
	return accountScopeMap(actor, accounts)[accountID], nil
}

func (s *Service) deviceInScope(actor *AdminAccount, deviceID string) (bool, error) {
	devices, err := s.ListDevicesFor(actor)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if device.DeviceID == strings.TrimSpace(deviceID) {
			return true, nil
		}
	}
	return false, sql.ErrNoRows
}

func accountScopeMap(actor *AdminAccount, accounts []AdminAccount) map[int64]bool {
	scope := map[int64]bool{actor.ID: true}
	changed := true
	for changed {
		changed = false
		for _, account := range accounts {
			if scope[account.ParentID] && !scope[account.ID] {
				scope[account.ID] = true
				changed = true
			}
		}
	}
	return scope
}

func hasPermission(account *AdminAccount, permission string) bool {
	if account == nil || account.Status != AdminAccountStatusActive {
		return false
	}
	for _, value := range account.Permissions {
		if value == permission || value == "*" {
			return true
		}
	}
	return false
}

func defaultPermissionsForLevel(level int) []string {
	switch level {
	case AdminLevelRoot:
		return allAdminPermissions()
	case AdminLevelReseller:
		return []string{
			PermissionCardsView,
			PermissionCardsGenerate,
			PermissionCardsDisable,
			PermissionDevicesView,
			PermissionDevicesRemark,
			PermissionDevicesExpiry,
			PermissionDevicesRemoteView,
			PermissionDevicesRemoteWrite,
			PermissionActivationsView,
			PermissionActivationsDisable,
			PermissionHistoryView,
			PermissionAccountsView,
			PermissionAccountsManage,
			PermissionAIAnalysisView,
			PermissionAIAnalysisRun,
			PermissionAIReportsView,
		}
	case AdminLevelDistributor:
		return []string{
			PermissionCardsView,
			PermissionCardsGenerate,
			PermissionCardsDisable,
			PermissionDevicesView,
			PermissionDevicesRemark,
			PermissionDevicesExpiry,
			PermissionDevicesRemoteView,
			PermissionDevicesRemoteWrite,
			PermissionActivationsView,
			PermissionActivationsDisable,
			PermissionHistoryView,
			PermissionAIAnalysisView,
			PermissionAIReportsView,
		}
	default:
		return []string{}
	}
}

func allAdminPermissions() []string {
	return []string{
		PermissionCardsView,
		PermissionCardsGenerate,
		PermissionCardsDisable,
		PermissionCardsDelete,
		PermissionDevicesView,
		PermissionDevicesRemark,
		PermissionDevicesExpiry,
		PermissionDevicesRemoteView,
		PermissionDevicesRemoteWrite,
		PermissionDevicesRemoteSecrets,
		PermissionActivationsView,
		PermissionActivationsDisable,
		PermissionHistoryView,
		PermissionAccountsView,
		PermissionAccountsManage,
		PermissionAIAnalysisView,
		PermissionAIAnalysisRun,
		PermissionAIReportsView,
		PermissionAISettingsManage,
	}
}

func normalizePermissions(permissions []string) []string {
	allowed := map[string]bool{}
	for _, permission := range allAdminPermissions() {
		allowed[permission] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" || !allowed[permission] || seen[permission] {
			continue
		}
		seen[permission] = true
		result = append(result, permission)
	}
	return result
}

func permissionsForActor(actor *AdminAccount, permissions []string) []string {
	normalized := normalizePermissions(permissions)
	if actor == nil || actor.Level == AdminLevelRoot {
		return normalized
	}
	result := make([]string, 0, len(normalized))
	for _, permission := range normalized {
		if hasPermission(actor, permission) {
			result = append(result, permission)
		}
	}
	return result
}

func normalizeRemoteCapabilities(capabilities []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || seen[capability] {
			continue
		}
		seen[capability] = true
		result = append(result, capability)
	}
	return result
}

func hashAdminPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func ResolveDurationDays(plan Plan, customDays int) (int, error) {
	switch plan {
	case PlanMonthly:
		return 30, nil
	case PlanQuarterly:
		return 90, nil
	case PlanHalfYear:
		return 180, nil
	case PlanYearly:
		return 365, nil
	case PlanCustom:
		if customDays <= 0 {
			return 0, fmt.Errorf("custom plan requires positive days")
		}
		return customDays, nil
	default:
		return 0, fmt.Errorf("unsupported license plan: %s", plan)
	}
}

func HashCardKey(cardKey string) string {
	sum := sha256.Sum256([]byte(normalizeCardKey(cardKey)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func PublicKeyFromString(encoded string) (ed25519.PublicKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err == nil && len(key) == ed25519.PublicKeySize {
		return ed25519.PublicKey(key), nil
	}
	key, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err == nil && len(key) == ed25519.PublicKeySize {
		return ed25519.PublicKey(key), nil
	}
	key, err = base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err == nil && len(key) == ed25519.PublicKeySize {
		return ed25519.PublicKey(key), nil
	}
	return nil, fmt.Errorf("invalid public key")
}

func PrivateKeyFromString(encoded string) (ed25519.PrivateKey, error) {
	for _, decoder := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		key, err := decoder.DecodeString(strings.TrimSpace(encoded))
		if err == nil && len(key) == ed25519.PrivateKeySize {
			return ed25519.PrivateKey(key), nil
		}
	}
	return nil, fmt.Errorf("invalid private key")
}

func (s *Service) resultFor(activation *ActivationRecord, now time.Time, message string) (*ActivationResult, error) {
	ticket, payload, err := s.signTicket(activation, now)
	if err != nil {
		return nil, err
	}
	remainingDays := 0
	if activation.ExpiresAt.After(now) {
		remainingDays = int(activation.ExpiresAt.Sub(now).Hours() / 24)
		if activation.ExpiresAt.Sub(now)%(24*time.Hour) > 0 {
			remainingDays++
		}
	}
	return &ActivationResult{
		Licensed:      now.Before(activation.ExpiresAt) || now.Equal(activation.ExpiresAt),
		LicenseID:     activation.CardID,
		ActivationID:  activation.ID,
		DeviceID:      activation.DeviceID,
		Plan:          payload.Plan,
		Status:        activation.Status,
		ExpiresAt:     payload.ExpiresAt,
		RemainingDays: remainingDays,
		NextCheckAt:   payload.NextCheckAt,
		GraceUntil:    payload.GraceUntil,
		Ticket:        ticket,
		Message:       message,
	}, nil
}

func (s *Service) signTicket(activation *ActivationRecord, now time.Time) (string, ticketPayload, error) {
	if len(s.privateKey) != ed25519.PrivateKeySize {
		return "", ticketPayload{}, fmt.Errorf("license private key is not configured")
	}
	card, err := s.store.GetCard(activation.CardID)
	if err != nil {
		return "", ticketPayload{}, err
	}
	payload := ticketPayload{
		Product:      ProductCCNexusPro,
		LicenseID:    activation.CardID,
		ActivationID: activation.ID,
		DeviceID:     activation.DeviceID,
		Plan:         card.Plan,
		ExpiresAt:    activation.ExpiresAt.UTC(),
		NextCheckAt:  now.Add(nextCheckInterval).UTC(),
		GraceUntil:   now.Add(offlineGracePeriod).UTC(),
		IssuedAt:     now.UTC(),
	}
	canonicalVariants, err := canonicalTicketVariants(payload)
	if err != nil {
		return "", ticketPayload{}, err
	}
	envelope := ticketEnvelope{
		Payload:   payload,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.privateKey, canonicalVariants[0])),
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", ticketPayload{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw), payload, nil
}

func (s *Service) decodeAndVerifyTicket(ticket string) (*ticketPayload, error) {
	if len(s.publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidTicket
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(ticket))
	if err != nil {
		return nil, ErrInvalidTicket
	}
	var envelope ticketEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, ErrInvalidTicket
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil {
		return nil, ErrInvalidTicket
	}
	canonicalVariants, err := canonicalTicketVariants(envelope.Payload)
	if err != nil {
		return nil, err
	}
	verified := false
	for _, canonical := range canonicalVariants {
		if ed25519.Verify(s.publicKey, canonical, signature) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, ErrInvalidTicket
	}
	return &envelope.Payload, nil
}

func canonicalTicketVariants(payload ticketPayload) ([][]byte, error) {
	type canonical struct {
		Product      string `json:"product"`
		LicenseID    int64  `json:"licenseId"`
		ActivationID int64  `json:"activationId"`
		DeviceID     string `json:"deviceId"`
		Plan         Plan   `json:"plan"`
		ExpiresAt    string `json:"expiresAt"`
		NextCheckAt  string `json:"nextCheckAt"`
		GraceUntil   string `json:"graceUntil"`
		IssuedAt     string `json:"issuedAt"`
	}
	type legacyCanonical struct {
		Product      string `json:"product"`
		LicenseID    int64  `json:"licenseId"`
		ActivationID int64  `json:"activationId"`
		DeviceID     string `json:"deviceId"`
		ExpiresAt    string `json:"expiresAt"`
		NextCheckAt  string `json:"nextCheckAt"`
		GraceUntil   string `json:"graceUntil"`
		IssuedAt     string `json:"issuedAt"`
	}
	current, err := json.Marshal(canonical{
		Product:      payload.Product,
		LicenseID:    payload.LicenseID,
		ActivationID: payload.ActivationID,
		DeviceID:     payload.DeviceID,
		Plan:         payload.Plan,
		ExpiresAt:    payload.ExpiresAt.UTC().Format(time.RFC3339Nano),
		NextCheckAt:  payload.NextCheckAt.UTC().Format(time.RFC3339Nano),
		GraceUntil:   payload.GraceUntil.UTC().Format(time.RFC3339Nano),
		IssuedAt:     payload.IssuedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	legacy, err := json.Marshal(legacyCanonical{
		Product:      payload.Product,
		LicenseID:    payload.LicenseID,
		ActivationID: payload.ActivationID,
		DeviceID:     payload.DeviceID,
		ExpiresAt:    payload.ExpiresAt.UTC().Format(time.RFC3339Nano),
		NextCheckAt:  payload.NextCheckAt.UTC().Format(time.RFC3339Nano),
		GraceUntil:   payload.GraceUntil.UTC().Format(time.RFC3339Nano),
		IssuedAt:     payload.IssuedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	return [][]byte{current, legacy}, nil
}

func (s *Service) currentTime() time.Time {
	now := s.now()
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC()
}

func randomCardKey() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return cardPrefix + group(encoded, 5), nil
}

func normalizeCardKey(cardKey string) string {
	upper := strings.ToUpper(strings.TrimSpace(cardKey))
	replacer := strings.NewReplacer("-", "", " ", "", "\n", "", "\r", "", "\t", "")
	return replacer.Replace(upper)
}

func group(value string, size int) string {
	if size <= 0 {
		return value
	}
	var buf strings.Builder
	for i, r := range value {
		if i > 0 && i%size == 0 {
			buf.WriteByte('-')
		}
		buf.WriteRune(r)
	}
	return buf.String()
}

func cardsCSV(cards []GeneratedCard) (string, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write([]string{"cardKey", "plan", "days", "maxDevices", "customer", "remark", "createdAt"}); err != nil {
		return "", err
	}
	for _, card := range cards {
		if err := writer.Write([]string{
			card.CardKey,
			string(card.Plan),
			strconv.Itoa(card.Days),
			strconv.Itoa(card.MaxDevices),
			card.Customer,
			card.Remark,
			card.CreatedAt.Format(time.RFC3339),
		}); err != nil {
			return "", err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
