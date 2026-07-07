package storage_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/fabrizio/copilot-claude-proxy/internal/storage"
)

// fakeBackend is a mutex-guarded in-memory keyring with an injectable error.
type fakeBackend struct {
	mu      sync.Mutex
	secrets map[string]string
	err     error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{secrets: map[string]string{}}
}

func (f *fakeBackend) Get(service, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	secret, ok := f.secrets[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (f *fakeBackend) Set(service, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.secrets[service+"/"+user] = password
	return nil
}

func (f *fakeBackend) Delete(service, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, ok := f.secrets[service+"/"+user]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.secrets, service+"/"+user)
	return nil
}

func TestSaveAndLoad(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	store := storage.NewTokenStoreWith(backend)

	if err := store.Save("gho_secret"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	token, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if token != "gho_secret" {
		t.Errorf("Load = %q, want gho_secret", token)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	store := storage.NewTokenStoreWith(backend)

	if err := store.Save("  gho_secret\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	token, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if token != "gho_secret" {
		t.Errorf("Load = %q, want trimmed gho_secret", token)
	}
}

func TestLoadEmptyWhenAbsent(t *testing.T) {
	t.Parallel()
	store := storage.NewTokenStoreWith(newFakeBackend())

	token, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if token != "" {
		t.Errorf("Load = %q, want empty", token)
	}
}

func TestLoadSurfacesKeyringFailure(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.err = errors.New("dbus: no session bus")
	store := storage.NewTokenStoreWith(backend)

	if _, err := store.Load(); err == nil {
		t.Error("Load with a broken keyring should return an error")
	}
}

func TestSaveSurfacesKeyringFailure(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	backend.err = errors.New("dbus: no session bus")
	store := storage.NewTokenStoreWith(backend)

	if err := store.Save("gho_secret"); err == nil {
		t.Error("Save with a broken keyring should return an error")
	}
}

func TestClear(t *testing.T) {
	t.Parallel()
	backend := newFakeBackend()
	store := storage.NewTokenStoreWith(backend)

	if err := store.Save("gho_secret"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	removed, err := store.Clear()
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if !removed {
		t.Error("Clear = false, want true for a stored token")
	}

	again, err := store.Clear()
	if err != nil {
		t.Fatalf("Clear (second): %v", err)
	}
	if again {
		t.Error("Clear = true on an empty keyring, want false")
	}

	token, err := store.Load()
	if err != nil {
		t.Fatalf("Load after Clear: %v", err)
	}
	if token != "" {
		t.Errorf("Load after Clear = %q, want empty", token)
	}
}
