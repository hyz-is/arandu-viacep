// Package viacep finds the address of a Brazilian postcode, so a form asks for
// the CEP and the number and fills the rest: the street, the district, the
// city and the state.
//
// It asks ViaCEP first and BrasilAPI when ViaCEP does not answer, keeps what
// it learned in memory for a day, and answers the application in Go (Lookup)
// and the browser over one route (GET /cep/{cep}, as JSON). The route is
// public on purpose: a checkout's buyer has no session, and a postcode's
// address is public data. Limiting how often one address may ask is the
// application's rate limiter's job, in front of every public route.
//
// An address found here is a suggestion for a person to confirm, never a fact
// the application stores without them: a street is renamed, a new CEP is not
// in either base yet, and a CEP for a whole small town names no street.
package viacep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// ErrInvalid is a value that is not eight digits.
	ErrInvalid = errors.New("viacep: a CEP is eight digits")
	// ErrNotFound is a CEP no base knows.
	ErrNotFound = errors.New("viacep: no base knows this CEP")
	// ErrUnavailable is no base answering.
	ErrUnavailable = errors.New("viacep: no base answered")
)

// Address is what a CEP names. Street and District are empty for a CEP that
// covers a whole town.
type Address struct {
	CEP        string `json:"cep"`
	Street     string `json:"street"`
	Complement string `json:"complement,omitempty"`
	District   string `json:"district"`
	City       string `json:"city"`
	State      string `json:"state"`
	IBGE       string `json:"ibge,omitempty"`
}

// Normalize answers the eight digits of a CEP written as 00000-000,
// 00000000 or with spaces, or ErrInvalid.
func Normalize(raw string) (string, error) {
	var digits strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '-' || r == '.' || r == ' ':
		default:
			return "", ErrInvalid
		}
	}
	if digits.Len() != 8 {
		return "", ErrInvalid
	}
	return digits.String(), nil
}

// Provider is one base of addresses.
type Provider interface {
	// Lookup answers the address of an eight-digit CEP, ErrNotFound when the
	// base does not know it, or another error when it did not answer.
	Lookup(ctx context.Context, cep string) (Address, error)
}

// ViaCEP is viacep.com.br. BaseURL is for a test; empty is the public one.
type ViaCEP struct {
	Client  *http.Client
	BaseURL string
}

// Lookup asks ViaCEP, which answers {"erro": true} for a CEP it does not know.
func (v ViaCEP) Lookup(ctx context.Context, cep string) (Address, error) {
	base := strings.TrimRight(v.BaseURL, "/")
	if base == "" {
		base = "https://viacep.com.br"
	}
	var body struct {
		CEP         string `json:"cep"`
		Logradouro  string `json:"logradouro"`
		Complemento string `json:"complemento"`
		Bairro      string `json:"bairro"`
		Localidade  string `json:"localidade"`
		UF          string `json:"uf"`
		IBGE        string `json:"ibge"`
		Erro        any    `json:"erro"`
	}
	status, err := getJSON(ctx, v.Client, base+"/ws/"+cep+"/json/", &body)
	if err != nil {
		return Address{}, err
	}
	if status == http.StatusBadRequest || status == http.StatusNotFound || body.Erro != nil {
		return Address{}, ErrNotFound
	}
	if status != http.StatusOK || body.Localidade == "" {
		return Address{}, fmt.Errorf("%w: viacep HTTP %d", ErrUnavailable, status)
	}
	return Address{CEP: cep, Street: body.Logradouro, Complement: body.Complemento, District: body.Bairro,
		City: body.Localidade, State: body.UF, IBGE: body.IBGE}, nil
}

// BrasilAPI is brasilapi.com.br, which aggregates several bases. BaseURL is
// for a test; empty is the public one.
type BrasilAPI struct {
	Client  *http.Client
	BaseURL string
}

// Lookup asks BrasilAPI's CEP v2, which answers 404 for a CEP it does not know.
func (b BrasilAPI) Lookup(ctx context.Context, cep string) (Address, error) {
	base := strings.TrimRight(b.BaseURL, "/")
	if base == "" {
		base = "https://brasilapi.com.br"
	}
	var body struct {
		CEP          string `json:"cep"`
		State        string `json:"state"`
		City         string `json:"city"`
		Neighborhood string `json:"neighborhood"`
		Street       string `json:"street"`
	}
	status, err := getJSON(ctx, b.Client, base+"/api/cep/v2/"+cep, &body)
	if err != nil {
		return Address{}, err
	}
	if status == http.StatusNotFound {
		return Address{}, ErrNotFound
	}
	if status != http.StatusOK || body.City == "" {
		return Address{}, fmt.Errorf("%w: brasilapi HTTP %d", ErrUnavailable, status)
	}
	return Address{CEP: cep, Street: body.Street, District: body.Neighborhood, City: body.City, State: body.State}, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, into any) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return res.StatusCode, fmt.Errorf("%w: unreadable answer", ErrUnavailable)
	}
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, into); err != nil {
			return res.StatusCode, fmt.Errorf("%w: invalid answer", ErrUnavailable)
		}
	}
	return res.StatusCode, nil
}

// Finder asks its providers in order and remembers what they answered.
//
// A provider that does not know a CEP is not the last word -- a new CEP
// reaches one base before the other -- so the next is asked; the CEP is not
// found when none knows it, and the bases are unavailable when none answered.
// A found address is kept for TTL and a CEP no base knows for a tenth of it,
// so a typo is not asked of the bases on every keystroke.
type Finder struct {
	providers []Provider
	ttl       time.Duration
	limit     int
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	address Address
	err     error
	until   time.Time
}

// NewFinder asks providers in order; none means ViaCEP and then BrasilAPI.
// A ttl of zero is a day, and limit is how many CEPs it keeps (zero is ten
// thousand).
func NewFinder(ttl time.Duration, limit int, providers ...Provider) *Finder {
	if len(providers) == 0 {
		providers = []Provider{ViaCEP{}, BrasilAPI{}}
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if limit <= 0 {
		limit = 10_000
	}
	return &Finder{providers: providers, ttl: ttl, limit: limit, now: time.Now, cache: map[string]cached{}}
}

// Lookup answers the address of a CEP written in any of the usual ways.
func (f *Finder) Lookup(ctx context.Context, raw string) (Address, error) {
	cep, err := Normalize(raw)
	if err != nil {
		return Address{}, err
	}
	f.mu.Lock()
	hit, ok := f.cache[cep]
	f.mu.Unlock()
	if ok && f.now().Before(hit.until) {
		return hit.address, hit.err
	}
	found, notFound := Address{}, false
	var lastErr error
	for _, provider := range f.providers {
		address, err := provider.Lookup(ctx, cep)
		if err == nil {
			found = address
			lastErr = nil
			break
		}
		if errors.Is(err, ErrNotFound) {
			notFound = true
		}
		lastErr = err
	}
	switch {
	case lastErr == nil:
		f.keep(cep, cached{address: found, until: f.now().Add(f.ttl)})
		return found, nil
	case notFound:
		f.keep(cep, cached{err: ErrNotFound, until: f.now().Add(f.ttl / 10)})
		return Address{}, ErrNotFound
	}
	return Address{}, fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
}

func (f *Finder) keep(cep string, entry cached) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cache) >= f.limit {
		// Full: drop what has expired, and everything if nothing has.
		now := f.now()
		for key, old := range f.cache {
			if !now.Before(old.until) {
				delete(f.cache, key)
			}
		}
		if len(f.cache) >= f.limit {
			f.cache = map[string]cached{}
		}
	}
	f.cache[cep] = entry
}
