package onlinelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type remoteCommandCanonical struct {
	Version         string `json:"version"`
	DeviceID        string `json:"deviceId"`
	CommandNonce    string `json:"commandNonce"`
	CommandType     string `json:"commandType"`
	ServerPublicKey string `json:"serverPublicKey"`
	EnvelopeNonce   string `json:"envelopeNonce"`
	Ciphertext      string `json:"ciphertext"`
	ExpiresAt       string `json:"expiresAt"`
	CreatedAt       string `json:"createdAt"`
}

func signRemoteCommand(privateKey ed25519.PrivateKey, command *RemoteCommandRecord) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("license private key is not configured")
	}
	if command == nil {
		return fmt.Errorf("remote command is required")
	}
	if strings.TrimSpace(command.CommandNonce) == "" {
		nonce, err := randomRemoteCommandNonce()
		if err != nil {
			return err
		}
		command.CommandNonce = nonce
	}
	canonical, err := canonicalRemoteCommand(command)
	if err != nil {
		return err
	}
	command.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, canonical))
	return nil
}

func (s *Service) VerifyRemoteCommand(command RemoteCommandRecord, deviceID string, now time.Time) error {
	if s == nil || len(s.publicKey) != ed25519.PublicKeySize {
		return ErrInvalidTicket
	}
	if strings.TrimSpace(command.Signature) == "" || strings.TrimSpace(command.CommandNonce) == "" {
		return fmt.Errorf("unsigned remote command")
	}
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(command.DeviceID) != strings.TrimSpace(deviceID) {
		return fmt.Errorf("remote command device mismatch")
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	if command.ExpiresAt.IsZero() || !now.UTC().Before(command.ExpiresAt.UTC()) {
		return fmt.Errorf("remote command expired")
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(command.Signature))
	if err != nil {
		return fmt.Errorf("invalid remote command signature")
	}
	canonical, err := canonicalRemoteCommand(&command)
	if err != nil {
		return err
	}
	if !ed25519.Verify(s.publicKey, canonical, signature) {
		return fmt.Errorf("invalid remote command signature")
	}
	return nil
}

func canonicalRemoteCommand(command *RemoteCommandRecord) ([]byte, error) {
	if command == nil {
		return nil, fmt.Errorf("remote command is required")
	}
	payload := remoteCommandCanonical{
		Version:         RemoteCommandSignatureVersion,
		DeviceID:        strings.TrimSpace(command.DeviceID),
		CommandNonce:    strings.TrimSpace(command.CommandNonce),
		CommandType:     strings.TrimSpace(command.CommandType),
		ServerPublicKey: strings.TrimSpace(command.Envelope.ServerPublicKey),
		EnvelopeNonce:   strings.TrimSpace(command.Envelope.Nonce),
		Ciphertext:      strings.TrimSpace(command.Envelope.Ciphertext),
		ExpiresAt:       command.ExpiresAt.UTC().Format(time.RFC3339Nano),
		CreatedAt:       command.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if payload.DeviceID == "" || payload.CommandNonce == "" || payload.CommandType == "" ||
		payload.ServerPublicKey == "" || payload.EnvelopeNonce == "" || payload.Ciphertext == "" ||
		command.ExpiresAt.IsZero() || command.CreatedAt.IsZero() {
		return nil, fmt.Errorf("incomplete remote command signature payload")
	}
	return json.Marshal(payload)
}

func randomRemoteCommandNonce() (string, error) {
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
