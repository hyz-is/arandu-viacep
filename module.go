package viacep

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// DefaultPrefix is where the route is mounted when Config leaves Prefix empty.
const DefaultPrefix = "/cep"

// Config is what the application passes when it wires this package.
type Config struct {
	// Prefix is the path the route is mounted under: GET {Prefix}/{cep}.
	// Empty means DefaultPrefix.
	Prefix string
	// Providers are the bases asked, in order. Empty is ViaCEP and then
	// BrasilAPI, over a client with a four-second timeout.
	Providers []Provider
	// TTL is how long an address found is kept; zero is a day. Limit is how
	// many CEPs are kept; zero is ten thousand.
	TTL   time.Duration
	Limit int
}

// Module is what the application registers: one public route answering the
// address of a CEP as JSON, and Lookup for the application's own code.
type Module struct {
	prefix string
	finder *Finder
}

var _ foundation.Module = (*Module)(nil)

// New builds the module.
func New(cfg Config) (*Module, error) {
	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if !strings.HasPrefix(prefix, "/") {
		return nil, errors.New("viacep: Config.Prefix has to start with /")
	}
	return &Module{prefix: prefix, finder: NewFinder(cfg.TTL, cfg.Limit, cfg.Providers...)}, nil
}

// Name is the module's identifier.
func (m *Module) Name() string { return "cep" }

// Routes registers GET {prefix}/{cep}, named cep.show.
func (m *Module) Routes(r *fhttp.Router) {
	r.Action(http.MethodGet, m.prefix+"/{cep}", m.show).Name("cep.show")
}

// Lookup answers the address of a CEP, from the cache or the bases.
func (m *Module) Lookup(ctx context.Context, raw string) (Address, error) {
	return m.finder.Lookup(ctx, raw)
}

// Answer is what the route answers for a CEP: the status and the body. It is
// separate from the handler so the answer is the same in a test and on the
// wire.
func (m *Module) Answer(ctx context.Context, raw string) (int, any) {
	address, err := m.finder.Lookup(ctx, raw)
	switch {
	case err == nil:
		return http.StatusOK, address
	case errors.Is(err, ErrInvalid):
		return http.StatusUnprocessableEntity, map[string]string{"error": "invalid_cep"}
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, map[string]string{"error": "cep_not_found"}
	}
	return http.StatusServiceUnavailable, map[string]string{"error": "cep_unavailable"}
}

func (m *Module) show(ctx *fhttp.Context) error {
	status, body := m.Answer(ctx.Ctx(), ctx.Param("cep"))
	if status == http.StatusOK {
		// An address changes rarely; a browser may keep it for a day.
		ctx.Response.Header().Set("Cache-Control", "public, max-age=86400")
	}
	ctx.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	ctx.Response.WriteHeader(status)
	return json.NewEncoder(ctx.Response).Encode(body)
}
