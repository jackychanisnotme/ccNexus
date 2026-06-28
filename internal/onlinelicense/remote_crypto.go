package onlinelicense

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"
)

const (
	remoteCryptoInfo       = "ainexus-license-remote-v1"
	remoteRevealCryptoInfo = "ainexus-license-remote-reveal-v1"
	remoteRevealAlgorithm  = "P-256-HKDF-SHA256-AES-256-GCM"
)

func EncodeRemotePublicKey(key *ecdh.PublicKey) string {
	if key == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(key.Bytes())
}

func DecodeRemotePublicKey(value string) (*ecdh.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPublicKey(raw)
}

func EncryptRemoteEnvelope(devicePublicKey string, plaintext []byte) (RemoteEnvelope, error) {
	peer, err := DecodeRemotePublicKey(devicePublicKey)
	if err != nil {
		return RemoteEnvelope{}, err
	}
	serverKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return RemoteEnvelope{}, err
	}
	shared, err := serverKey.ECDH(peer)
	if err != nil {
		return RemoteEnvelope{}, err
	}
	key, err := deriveRemoteKey(shared, serverKey.PublicKey().Bytes(), peer.Bytes())
	if err != nil {
		return RemoteEnvelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return RemoteEnvelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return RemoteEnvelope{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return RemoteEnvelope{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(remoteCryptoInfo))
	return RemoteEnvelope{
		ServerPublicKey: EncodeRemotePublicKey(serverKey.PublicKey()),
		Nonce:           base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:      base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}

func DecryptRemoteEnvelope(devicePrivateKey *ecdh.PrivateKey, envelope RemoteEnvelope) ([]byte, error) {
	if devicePrivateKey == nil {
		return nil, fmt.Errorf("device private key is required")
	}
	serverPublicKey, err := DecodeRemotePublicKey(envelope.ServerPublicKey)
	if err != nil {
		return nil, err
	}
	shared, err := devicePrivateKey.ECDH(serverPublicKey)
	if err != nil {
		return nil, err
	}
	key, err := deriveRemoteKey(shared, serverPublicKey.Bytes(), devicePrivateKey.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(remoteCryptoInfo))
}

func deriveRemoteKey(shared, senderPublic, receiverPublic []byte) ([]byte, error) {
	salt := append(append([]byte{}, senderPublic...), receiverPublic...)
	reader := hkdf.New(sha256.New, shared, salt, []byte(remoteCryptoInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func EncodeRemoteRevealPublicKey(key *ecdh.PublicKey) string {
	if key == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(key.Bytes())
}

func DecodeRemoteRevealPublicKey(value string) (*ecdh.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return ecdh.P256().NewPublicKey(raw)
}

func EncryptRemoteSecretRevealResult(adminPublicKey string, plaintext RemoteSecretRevealPlaintext, expiresAt time.Time) (*RemoteSecretRevealResult, error) {
	peer, err := DecodeRemoteRevealPublicKey(adminPublicKey)
	if err != nil {
		return nil, err
	}
	clientKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(plaintext)
	if err != nil {
		return nil, err
	}
	shared, err := clientKey.ECDH(peer)
	if err != nil {
		return nil, err
	}
	key, err := deriveRemoteRevealKey(shared, clientKey.PublicKey().Bytes(), peer.Bytes())
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return &RemoteSecretRevealResult{
		Algorithm:       remoteRevealAlgorithm,
		ClientPublicKey: EncodeRemoteRevealPublicKey(clientKey.PublicKey()),
		Nonce:           base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:      base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, nonce, data, []byte(remoteRevealCryptoInfo))),
		ExpiresAt:       expiresAt.UTC(),
	}, nil
}

func DecryptRemoteSecretRevealResult(adminPrivateKey *ecdh.PrivateKey, result RemoteSecretRevealResult) (*RemoteSecretRevealPlaintext, error) {
	if adminPrivateKey == nil {
		return nil, fmt.Errorf("admin private key is required")
	}
	if result.Algorithm != "" && result.Algorithm != remoteRevealAlgorithm {
		return nil, fmt.Errorf("unsupported reveal algorithm")
	}
	clientPublicKey, err := DecodeRemoteRevealPublicKey(result.ClientPublicKey)
	if err != nil {
		return nil, err
	}
	shared, err := adminPrivateKey.ECDH(clientPublicKey)
	if err != nil {
		return nil, err
	}
	key, err := deriveRemoteRevealKey(shared, clientPublicKey.Bytes(), adminPrivateKey.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(result.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(result.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(remoteRevealCryptoInfo))
	if err != nil {
		return nil, err
	}
	var decoded RemoteSecretRevealPlaintext
	if err := json.Unmarshal(plain, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func deriveRemoteRevealKey(shared, senderPublic, receiverPublic []byte) ([]byte, error) {
	salt := append(append([]byte{}, senderPublic...), receiverPublic...)
	reader := hkdf.New(sha256.New, shared, salt, []byte(remoteRevealCryptoInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}
