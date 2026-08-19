package copilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// AccountSession associates a stored GitHub login with its Copilot session.
type AccountSession struct {
	Name    string
	Session *Session
}

// AccountUsage summarizes upstream requests made with one account.
type AccountUsage struct {
	Name        string `json:"name"`
	Requests    uint64 `json:"requests"`
	RateLimited uint64 `json:"rate_limited"`
}

// PoolUsage is the runtime usage snapshot exposed by the stats endpoint.
type PoolUsage struct {
	Current   string         `json:"current_account"`
	Requests  uint64         `json:"requests"`
	Failovers uint64         `json:"failovers"`
	Accounts  []AccountUsage `json:"accounts"`
}

// AccountPool sends requests through the current account and rotates to the
// next account when Copilot reports quota or rate-limit exhaustion with 429.
type AccountPool struct {
	mu             sync.Mutex
	accounts       []AccountSession
	current        int
	manualRevision uint64
	requests       uint64
	failovers      uint64
	usage          []AccountUsage
}

// NewAccountPool builds a pool from already-connected account sessions.
func NewAccountPool(accounts []AccountSession) (*AccountPool, error) {
	if len(accounts) == 0 {
		return nil, errors.New("at least one Copilot account is required")
	}
	pool := &AccountPool{
		accounts: append([]AccountSession(nil), accounts...),
		usage:    make([]AccountUsage, len(accounts)),
	}
	for index, account := range accounts {
		if account.Session == nil || account.Session.Client == nil {
			return nil, errors.New("Copilot account session is not connected")
		}
		pool.usage[index].Name = account.Name
	}
	return pool, nil
}

// Do implements the server's Copilot caller. A request is attempted at most
// once per account, preserving the final 429 response when every account is limited.
func (p *AccountPool) Do(ctx context.Context, opts CallOptions) (*http.Response, error) {
	p.mu.Lock()
	start := p.current
	manualRevision := p.manualRevision
	count := len(p.accounts)
	p.mu.Unlock()

	for attempt := 0; attempt < count; attempt++ {
		index := (start + attempt) % count
		p.recordRequest(index)
		resp, err := p.accounts[index].Session.Client.Do(ctx, opts)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		p.recordRateLimit(index)
		if attempt == count-1 {
			return resp, nil
		}
		_ = resp.Body.Close()
		p.recordFailover((index+1)%count, manualRevision)
	}
	return nil, errors.New("no Copilot account available")
}

// SwitchAccount selects the account used for the next upstream request.
func (p *AccountPool) SwitchAccount(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, account := range p.accounts {
		if strings.EqualFold(account.Name, strings.TrimSpace(name)) {
			p.current = index
			p.manualRevision++
			return nil
		}
	}
	return fmt.Errorf("account %q is not connected", name)
}

// Usage returns a race-free snapshot of the current runtime statistics.
func (p *AccountPool) Usage() PoolUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PoolUsage{
		Current:   p.accounts[p.current].Name,
		Requests:  p.requests,
		Failovers: p.failovers,
		Accounts:  append([]AccountUsage(nil), p.usage...),
	}
}

// TokenValid reports whether at least one account has a usable cached token.
func (p *AccountPool) TokenValid() bool {
	for _, account := range p.accounts {
		if account.Session.Tokens.TokenValid() {
			return true
		}
	}
	return false
}

// Sessions returns the connected sessions for background refresh loops.
func (p *AccountPool) Sessions() []AccountSession {
	return append([]AccountSession(nil), p.accounts...)
}

func (p *AccountPool) recordRequest(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	p.usage[index].Requests++
}

func (p *AccountPool) recordRateLimit(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage[index].RateLimited++
}

func (p *AccountPool) recordFailover(next int, manualRevision uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failovers++
	if p.manualRevision == manualRevision {
		p.current = next
	}
}
