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
	"strconv"
	"strings"
	"time"
)

const (
	cardPrefix         = "CCNX-ONL-"
	nextCheckInterval  = 24 * time.Hour
	offlineGracePeriod = 30 * 24 * time.Hour
)

var (
	ErrInvalidCard       = errors.New("invalid license card")
	ErrCardDisabled      = errors.New("license card is disabled")
	ErrDeviceLimit       = errors.New("license card device limit reached")
	ErrActivationBlocked = errors.New("license activation is disabled")
	ErrInvalidTicket     = errors.New("invalid license ticket")
	ErrTicketExpired     = errors.New("license ticket grace period expired")
)

type Store interface {
	CreateCard(card *CardRecord) error
	FindCardByHash(hash string) (*CardRecord, error)
	GetCard(id int64) (*CardRecord, error)
	ListCards() ([]CardRecord, error)
	DisableCard(id int64, now time.Time) error
	DeleteCard(id int64) error
	ActivateCard(cardHash, deviceID string, now time.Time, platform, appVersion, ipAddress string) (*ActivationRecord, error)
	ActiveActivationCount(cardID int64) (int, error)
	FindActivation(cardID int64, deviceID string) (*ActivationRecord, error)
	LatestActivationForDevice(deviceID string) (*ActivationRecord, error)
	GetActivation(id int64) (*ActivationRecord, error)
	ListActivations() ([]ActivationRecord, error)
	UpsertActivation(activation *ActivationRecord) error
	TouchActivation(id int64, now time.Time, platform, appVersion, ipAddress string) error
	DisableActivation(id int64, now time.Time) error
	SetDeviceExpiry(deviceID string, expiresAt, now time.Time) error
	ListDeviceRemarks() (map[string]string, error)
	SetDeviceRemark(deviceID, remark string, now time.Time) error
	AddAudit(action, targetType string, targetID int64, detail string, now time.Time) error
	ListAudit(limit int) ([]AuditRecord, error)
}

type Service struct {
	store      Store
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	now        func() time.Time
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
	service := &Service{store: store, privateKey: privateKey, now: time.Now}
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
			CardHash:   HashCardKey(cardKey),
			Plan:       req.Plan,
			Days:       days,
			MaxDevices: maxDevices,
			Status:     CardStatusActive,
			Customer:   strings.TrimSpace(req.Customer),
			Remark:     strings.TrimSpace(req.Remark),
			CreatedAt:  now,
		}
		if err := s.store.CreateCard(card); err != nil {
			return nil, err
		}
		detail := fmt.Sprintf("plan=%s days=%d maxDevices=%d customer=%s remark=%s", card.Plan, card.Days, card.MaxDevices, card.Customer, card.Remark)
		if err := s.store.AddAudit("generate_card", "card", card.ID, detail, now); err != nil {
			return nil, err
		}
		result.Cards = append(result.Cards, GeneratedCard{
			ID:         card.ID,
			CardKey:    cardKey,
			CardHash:   card.CardHash,
			Plan:       card.Plan,
			Days:       card.Days,
			MaxDevices: card.MaxDevices,
			Customer:   card.Customer,
			Remark:     card.Remark,
			Status:     card.Status,
			CreatedAt:  card.CreatedAt,
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

func (s *Service) ListActivations() ([]ActivationRecord, error) {
	return s.store.ListActivations()
}

func (s *Service) ListDevices() ([]DeviceRecord, error) {
	activations, err := s.store.ListActivations()
	if err != nil {
		return nil, err
	}
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

func (s *Service) RecordAudit(action, targetType string, targetID int64, detail string) error {
	return s.store.AddAudit(strings.TrimSpace(action), strings.TrimSpace(targetType), targetID, strings.TrimSpace(detail), s.currentTime())
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
