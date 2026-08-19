// Package storage persists the GitHub OAuth token in the operating system
// keyring: Secret Service on Linux/BSD, Keychain on macOS, and Credential
// Manager on Windows. Systems without a keyring can supply the token via the
// --github-token flag or the GH_TOKEN environment variable instead.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService   = "copilot-claude-proxy"
	keyringUser      = "github-token"
	keyringAccounts  = "github-accounts"
	accountKeyPrefix = "github-token:"
)

// Account is a stored GitHub identity and its OAuth token.
type Account struct {
	Name  string
	Token string
}

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

// SaveAccount stores a token under a GitHub login and adds it to the account list.
func (s *TokenStore) SaveAccount(name, token string) error {
	name = normalizeAccountName(name)
	if name == "" {
		return errors.New("account name is required")
	}
	if err := s.backend.Set(keyringService, accountKeyPrefix+name, token); err != nil {
		return fmt.Errorf("store token for account %q in the system keyring: %w", name, err)
	}

	names, err := s.accountNames()
	if err != nil {
		return err
	}
	if !slices.Contains(names, name) {
		names = append(names, name)
		slices.Sort(names)
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encode account list: %w", err)
	}
	if err := s.backend.Set(keyringService, keyringAccounts, string(encoded)); err != nil {
		return fmt.Errorf("store account list in the system keyring: %w", err)
	}
	return nil
}

// Accounts returns all named accounts. The legacy token is returned as
// "default" only when no named accounts have been configured.
func (s *TokenStore) Accounts() ([]Account, error) {
	names, err := s.accountNames()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		token, loadErr := s.Load()
		if loadErr != nil || token == "" {
			return nil, loadErr
		}
		return []Account{{Name: "default", Token: token}}, nil
	}

	accounts := make([]Account, 0, len(names))
	for _, name := range names {
		token, loadErr := s.loadAccount(name)
		if loadErr != nil {
			return nil, loadErr
		}
		if token != "" {
			accounts = append(accounts, Account{Name: name, Token: token})
		}
	}
	return accounts, nil
}

// ClearAccount removes one named account from the keyring.
func (s *TokenStore) ClearAccount(name string) (bool, error) {
	name = normalizeAccountName(name)
	names, err := s.accountNames()
	if err != nil {
		return false, err
	}
	if !slices.Contains(names, name) {
		return false, nil
	}
	if err := s.backend.Delete(keyringService, accountKeyPrefix+name); err != nil &&
		!errors.Is(err, keyring.ErrNotFound) {
		return false, fmt.Errorf("remove token for account %q: %w", name, err)
	}

	names = slices.DeleteFunc(names, func(candidate string) bool { return candidate == name })
	if len(names) == 0 {
		if err := s.backend.Delete(keyringService, keyringAccounts); err != nil &&
			!errors.Is(err, keyring.ErrNotFound) {
			return false, fmt.Errorf("remove account list: %w", err)
		}
		return true, nil
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return false, fmt.Errorf("encode account list: %w", err)
	}
	if err := s.backend.Set(keyringService, keyringAccounts, string(encoded)); err != nil {
		return false, fmt.Errorf("update account list: %w", err)
	}
	return true, nil
}

func (s *TokenStore) accountNames() ([]string, error) {
	raw, err := s.backend.Get(keyringService, keyringAccounts)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read account list from the system keyring: %w", err)
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("decode account list from the system keyring: %w", err)
	}
	return names, nil
}

func (s *TokenStore) loadAccount(name string) (string, error) {
	secret, err := s.backend.Get(keyringService, accountKeyPrefix+name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read token for account %q from the system keyring: %w", name, err)
	}
	return strings.TrimSpace(secret), nil
}

func normalizeAccountName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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
