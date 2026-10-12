package unit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	viacep "github.com/hyz-is/arandu-viacep"
)

func viaCEP(t *testing.T, answer func(w http.ResponseWriter, cep string)) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		code := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ws/"), "/json/")
		answer(w, code)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestNormalizeAcceptsTheUsualWaysOfWritingACEP(t *testing.T) {
	for _, raw := range []string{"01001-000", "01001000", " 01001 000 ", "01.001-000"} {
		got, err := viacep.Normalize(raw)
		if err != nil || got != "01001000" {
			t.Errorf("Normalize(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "0100100", "010010000", "01001-00a"} {
		if _, err := viacep.Normalize(raw); !errors.Is(err, viacep.ErrInvalid) {
			t.Errorf("Normalize(%q) did not refuse", raw)
		}
	}
}

func TestAFoundAddressIsAskedOnceAndKept(t *testing.T) {
	server, calls := viaCEP(t, func(w http.ResponseWriter, code string) {
		_, _ = w.Write([]byte(`{"cep":"01001-000","logradouro":"Praça da Sé","complemento":"lado ímpar","bairro":"Sé","localidade":"São Paulo","uf":"SP","ibge":"3550308"}`))
	})
	finder := viacep.NewFinder(0, 0, viacep.ViaCEP{Client: server.Client(), BaseURL: server.URL})
	for i := 0; i < 3; i++ {
		address, err := finder.Lookup(context.Background(), "01001-000")
		if err != nil {
			t.Fatal(err)
		}
		if address.Street != "Praça da Sé" || address.District != "Sé" || address.City != "São Paulo" || address.State != "SP" || address.CEP != "01001000" {
			t.Fatalf("address = %+v", address)
		}
	}
	if *calls != 1 {
		t.Errorf("the base was asked %d times for one CEP", *calls)
	}
}

func TestTheNextBaseIsAskedWhenTheFirstDoesNotKnowOrDoesNotAnswer(t *testing.T) {
	unknown, _ := viaCEP(t, func(w http.ResponseWriter, code string) { _, _ = w.Write([]byte(`{"erro": "true"}`)) })
	down, _ := viaCEP(t, func(w http.ResponseWriter, code string) { w.WriteHeader(http.StatusBadGateway) })
	brasil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cep/v2/89010025" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"cep":"89010025","state":"SC","city":"Blumenau","neighborhood":"Centro","street":"Rua Doutor Luiz de Freitas Melro"}`))
	}))
	defer brasil.Close()
	for name, first := range map[string]*httptest.Server{"unknown": unknown, "down": down} {
		finder := viacep.NewFinder(0, 0, viacep.ViaCEP{Client: first.Client(), BaseURL: first.URL}, viacep.BrasilAPI{Client: brasil.Client(), BaseURL: brasil.URL})
		address, err := finder.Lookup(context.Background(), "89010-025")
		if err != nil || address.City != "Blumenau" || address.State != "SC" {
			t.Errorf("%s: %+v, %v", name, address, err)
		}
	}
}

func TestACEPNoBaseKnowsIsNotFoundAndNoAnswerIsUnavailable(t *testing.T) {
	unknown, _ := viaCEP(t, func(w http.ResponseWriter, code string) { _, _ = w.Write([]byte(`{"erro": true}`)) })
	down, _ := viaCEP(t, func(w http.ResponseWriter, code string) { w.WriteHeader(http.StatusInternalServerError) })
	finder := viacep.NewFinder(0, 0, viacep.ViaCEP{Client: unknown.Client(), BaseURL: unknown.URL})
	if _, err := finder.Lookup(context.Background(), "99999999"); !errors.Is(err, viacep.ErrNotFound) {
		t.Errorf("unknown CEP: %v", err)
	}
	finder = viacep.NewFinder(0, 0, viacep.ViaCEP{Client: down.Client(), BaseURL: down.URL})
	if _, err := finder.Lookup(context.Background(), "01001000"); !errors.Is(err, viacep.ErrUnavailable) {
		t.Errorf("no answer: %v", err)
	}
}

func TestTheRouteAnswersEachCase(t *testing.T) {
	server, _ := viaCEP(t, func(w http.ResponseWriter, code string) {
		if code == "01001000" {
			_, _ = w.Write([]byte(`{"logradouro":"Praça da Sé","bairro":"Sé","localidade":"São Paulo","uf":"SP"}`))
			return
		}
		_, _ = w.Write([]byte(`{"erro": true}`))
	})
	module, err := viacep.New(viacep.Config{Providers: []viacep.Provider{viacep.ViaCEP{Client: server.Client(), BaseURL: server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]int{"01001-000": 200, "99999-999": 404, "123": 422} {
		if status, _ := module.Answer(context.Background(), raw); status != want {
			t.Errorf("%s: status %d, want %d", raw, status, want)
		}
	}
	if _, err := viacep.New(viacep.Config{Prefix: "cep"}); err == nil {
		t.Error("a prefix without / was accepted")
	}
}
