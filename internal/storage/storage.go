// Package storage persists the GitHub OAuth token in the operating system
// keyring: Secret Service on Linux/BSD, Keychain on macOS, and Credential
// Manager on Windows. Systems without a keyring can supply the token via the
// --github-token flag or the GH_TOKEN environment variable instead.
package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "copilot-claude-proxy"
	keyringUser    = "github-token"
)

// Backend is the keyring surface the store depends on; it matches the
// zalando/go-keyring package functions and is swappable in tests.
type Backend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

// systemKeyring adapts the zalando/go-keyring package functions.
type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user) //nolint:wrapcheck // sentinel errors must pass through unwrapped for errors.Is.
}

func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password) //nolint:wrapcheck // see Get.
}

func (systemKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user) //nolint:wrapcheck // see Get.
}

// TokenStore reads and writes the GitHub OAuth token in the OS keyring.
type TokenStore struct {
	backend Backend
}

// NewTokenStore returns a store backed by the operating system keyring.
func NewTokenStore() *TokenStore {
	return NewTokenStoreWith(systemKeyring{})
}

// NewTokenStoreWith builds a store with an explicit keyring backend.
func NewTokenStoreWith(backend Backend) *TokenStore {
	return &TokenStore{backend: backend}
}

// Load returns the stored token, or an empty string when none is stored.
func (s *TokenStore) Load() (string, error) {
	secret, err := s.backend.Get(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf(
			"read token from the system keyring"+
				" (on systems without one, pass --github-token or set GH_TOKEN): %w", err)
	}
	return strings.TrimSpace(secret), nil
}

// Save stores the token in the keyring.
func (s *TokenStore) Save(token string) error {
	if err := s.backend.Set(keyringService, keyringUser, token); err != nil {
		return fmt.Errorf(
			"store token in the system keyring"+
				" (on systems without one, pass --github-token or set GH_TOKEN): %w", err)
	}
	return nil
}

// Clear removes the stored token, reporting whether one was present.
func (s *TokenStore) Clear() (bool, error) {
	err := s.backend.Delete(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remove token from the system keyring: %w", err)
	}
	return true, nil
}
