package openbao

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{Address: server.URL, Token: "proxy-token"})
	if err != nil {
		t.Fatal(err)
	}

	return client
}

func TestCapabilitiesSelfUsesTheRequesterToken(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/capabilities-self" || r.Header.Get("X-Vault-Token") != "session-token" {
			t.Errorf("request %s with token %q", r.URL.Path, r.Header.Get("X-Vault-Token"))
		}

		var payload struct {
			Paths []string `json:"paths"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !slices.Equal(payload.Paths, []string{"git-proxy/acmecorp/widgetshop"}) {
			t.Errorf("paths = %v, %v", payload.Paths, err)
		}

		_, _ = w.Write([]byte(`{"data":{"capabilities":["read","update"],"git-proxy/acmecorp/widgetshop":["read","update"]}}`))
	})

	capabilities, err := client.CapabilitiesSelf(t.Context(), "session-token", "git-proxy/acmecorp/widgetshop")
	if err != nil || !slices.Equal(capabilities, []string{"read", "update"}) {
		t.Fatalf("capabilities = %v, %v", capabilities, err)
	}
}

func TestEveryCallUsesTheConfiguredNamespace(t *testing.T) {
	t.Setenv("BAO_NAMESPACE", "from-environment")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Namespace"); got != "team" {
			t.Errorf("%s: namespace %q, want team", r.URL.Path, got)
		}

		switch r.URL.Path {
		case "/v1/sys/capabilities-self":
			_, _ = w.Write([]byte(`{"data":{"git-proxy/a/b":["read"]}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"token":"ghp"}}`))
		}
	}))
	defer server.Close()

	client, err := New(Config{Address: server.URL, Namespace: "team", Token: "proxy-token"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.CapabilitiesSelf(t.Context(), "session-token", "git-proxy/a/b"); err != nil {
		t.Fatal(err)
	}

	if _, err := client.ReadField(t.Context(), "secret/github", "token"); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilitiesSelfReportsRefusedTokens(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	})

	if _, err := client.CapabilitiesSelf(t.Context(), "revoked", "git-proxy/a/b"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}

	unavailable := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := unavailable.CapabilitiesSelf(t.Context(), "token", "git-proxy/a/b"); err == nil || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want an error other than ErrInvalidToken", err)
	}
}

func TestReadFieldUsesTheProxyToken(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "proxy-token" {
			t.Errorf("token %q", r.Header.Get("X-Vault-Token"))
		}

		switch r.URL.Path {
		case "/v1/kv/data/git-proxy/github":
			_, _ = w.Write([]byte(`{"data":{"data":{"token":"ghp_kv2"},"metadata":{"version":3}}}`))
		case "/v1/secret/github":
			_, _ = w.Write([]byte(`{"data":{"token":"ghp_kv1"}}`))
		case "/v1/kv/data/empty":
			_, _ = w.Write([]byte(`{"data":{"data":{"token":""},"metadata":{"version":1}}}`))
		default:
			http.NotFound(w, r)
		}
	})

	for path, want := range map[string]string{"kv/data/git-proxy/github": "ghp_kv2", "secret/github": "ghp_kv1"} {
		if got, err := client.ReadField(t.Context(), path, "token"); err != nil || got != want {
			t.Fatalf("ReadField(%s) = %q, %v; want %q", path, got, err, want)
		}
	}

	for _, path := range []string{"kv/data/empty", "kv/data/missing"} {
		if _, err := client.ReadField(t.Context(), path, "token"); err == nil {
			t.Fatalf("ReadField(%s) succeeded", path)
		}
	}
}

func TestRenewSelfReturnsTheNewTTL(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/token/renew-self" || r.Header.Get("X-Vault-Token") != "proxy-token" {
			t.Errorf("request %s with token %q", r.URL.Path, r.Header.Get("X-Vault-Token"))
		}

		_, _ = w.Write([]byte(`{"auth":{"client_token":"proxy-token","lease_duration":2592000,"renewable":true}}`))
	})

	if ttl, err := client.RenewSelf(t.Context()); err != nil || ttl != 720*time.Hour {
		t.Fatalf("RenewSelf = %v, %v", ttl, err)
	}
}
