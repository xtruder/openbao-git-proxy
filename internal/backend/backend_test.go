package backend

import (
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/sdk/v2/logical"
)

type testMount struct {
	backend logical.Backend
	storage logical.Storage
}

func newTestMount(t *testing.T) *testMount {
	t.Helper()

	config := logical.TestBackendConfig()
	config.StorageView = new(logical.InmemStorage)
	config.Logger = hclog.NewNullLogger()
	backend, err := Factory(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}

	return &testMount{backend: backend, storage: config.StorageView}
}

// request runs one operation; a logical error response comes back as err.
func (m *testMount) request(t *testing.T, operation logical.Operation, path string, data map[string]any) (*logical.Response, error) {
	t.Helper()

	request := &logical.Request{Operation: operation, Path: path, Data: data, Storage: m.storage}
	if operation == logical.CreateOperation || operation == logical.UpdateOperation {
		exists, _, err := m.backend.HandleExistenceCheck(t.Context(), request)
		if err != nil {
			t.Fatalf("existence check %s: %v", path, err)
		}

		request.Operation = logical.UpdateOperation
		if !exists {
			request.Operation = logical.CreateOperation
		}
	}

	response, err := m.backend.HandleRequest(t.Context(), request)
	if err == nil && response != nil && response.IsError() {
		err = response.Error()
	}

	return response, err
}

func (m *testMount) mustWrite(t *testing.T, path string, data map[string]any) {
	t.Helper()

	if _, err := m.request(t, logical.UpdateOperation, path, data); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (m *testMount) list(t *testing.T, path string) []string {
	t.Helper()

	response, err := m.request(t, logical.ListOperation, path, nil)
	if err != nil {
		t.Fatalf("list %s: %v", path, err)
	}

	keys, _ := response.Data["keys"].([]string)
	return keys
}

func TestCredentialsHoldAUsernameAndPassword(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	m.mustWrite(t, "credential/default", map[string]any{"username": "x-access-token", "password": "ghp_secret"})
	response, err := m.request(t, logical.ReadOperation, "credential/default", nil)
	if err != nil || response.Data["username"] != "x-access-token" || response.Data["password"] != "ghp_secret" {
		t.Fatalf("read credential = %v, %v", response, err)
	}

	if keys := m.list(t, "credentials"); !slices.Equal(keys, []string{"default"}) {
		t.Fatalf("credentials = %v", keys)
	}

	for _, data := range []map[string]any{{"username": "x"}, {"password": "p"}} {
		if _, err := m.request(t, logical.UpdateOperation, "credential/other", data); err == nil {
			t.Fatalf("credential %v accepted", data)
		}
	}
}

func TestRepositoriesNameAnHTTPSURLAndACredential(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	m.mustWrite(t, "credential/default", map[string]any{"username": "x-access-token", "password": "ghp_secret"})
	m.mustWrite(t, "credential/gitlab", map[string]any{"username": "oauth2", "password": "glpat"})
	m.mustWrite(t, "repo/widgetshop", map[string]any{"url": "https://github.com/acmecorp/widgetshop.git"})
	m.mustWrite(t, "repo/infra", map[string]any{"url": "https://gitlab.example.com/ops/platform/infra.git", "credential": "gitlab"})

	response, err := m.request(t, logical.ReadOperation, "repo/widgetshop", nil)
	if err != nil || response.Data["url"] != "https://github.com/acmecorp/widgetshop.git" || response.Data["credential"] != "default" {
		t.Fatalf("read repo = %v, %v", response, err)
	}

	if keys := m.list(t, "repos"); !slices.Equal(keys, []string{"infra", "widgetshop"}) {
		t.Fatalf("repos = %v", keys)
	}

	for _, test := range []struct {
		path string
		data map[string]any
	}{
		{"repo/plain", map[string]any{"url": "http://github.com/a/b.git"}},
		{"repo/query", map[string]any{"url": "https://github.com/a/b.git?x=1"}},
		{"repo/userinfo", map[string]any{"url": "https://user:pw@github.com/a/b.git"}},
		{"repo/nohost", map[string]any{"url": "https:///a/b.git"}},
		{"repo/missing-credential", map[string]any{"url": "https://github.com/a/b.git", "credential": "nope"}},
		{"repo/Upper", map[string]any{"url": "https://github.com/a/b.git"}},
		{"repo/ends.git", map[string]any{"url": "https://github.com/a/b.git"}},
		{"repo/auth", map[string]any{"url": "https://github.com/a/b.git"}},
		{"repo/-dash", map[string]any{"url": "https://github.com/a/b.git"}},
	} {
		if _, err := m.request(t, logical.UpdateOperation, test.path, test.data); err == nil {
			t.Fatalf("%s %v accepted", test.path, test.data)
		}
	}
}

func TestUpdatesKeepFieldsTheyDontName(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	m.mustWrite(t, "config", map[string]any{"proxy_url": "https://git-proxy.example.com"})
	m.mustWrite(t, "credential/default", map[string]any{"username": "x-access-token", "password": "ghp_secret"})
	m.mustWrite(t, "credential/gitlab", map[string]any{"username": "oauth2", "password": "glpat"})
	m.mustWrite(t, "repo/infra", map[string]any{"url": "https://gitlab.example.com/ops/infra.git", "credential": "gitlab"})

	// Changing only the URL must not switch the repository to the default
	// credential, which would send the GitHub token to GitLab.
	m.mustWrite(t, "repo/infra", map[string]any{"url": "https://gitlab.example.com/ops/platform/infra.git"})
	response, err := m.request(t, logical.ReadOperation, "repo/infra", nil)
	if err != nil || response.Data["credential"] != "gitlab" || response.Data["url"] != "https://gitlab.example.com/ops/platform/infra.git" {
		t.Fatalf("after a URL-only update: %v, %v", response, err)
	}

	m.mustWrite(t, "repo/infra", map[string]any{"credential": "default"})
	if response, err = m.request(t, logical.ReadOperation, "repo/infra", nil); err != nil || response.Data["url"] != "https://gitlab.example.com/ops/platform/infra.git" {
		t.Fatalf("after a credential-only update: %v, %v", response, err)
	}

	m.mustWrite(t, "credential/gitlab", map[string]any{"password": "glpat-rotated"})
	if response, err = m.request(t, logical.ReadOperation, "credential/gitlab", nil); err != nil || response.Data["username"] != "oauth2" || response.Data["password"] != "glpat-rotated" {
		t.Fatalf("after a password-only update: %v, %v", response, err)
	}

	m.mustWrite(t, "config", map[string]any{})
	if response, err = m.request(t, logical.ReadOperation, "config", nil); err != nil || response.Data["proxy_url"] != "https://git-proxy.example.com" {
		t.Fatalf("after an empty config update: %v, %v", response, err)
	}

	if _, err := m.request(t, logical.UpdateOperation, "repo/new", map[string]any{"credential": "default"}); err == nil {
		t.Fatal("repository created without a url")
	}
}

func TestCredentialsInUseCantBeDeleted(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	m.mustWrite(t, "credential/default", map[string]any{"username": "x-access-token", "password": "ghp_secret"})
	m.mustWrite(t, "repo/widgetshop", map[string]any{"url": "https://github.com/acmecorp/widgetshop.git"})
	if _, err := m.request(t, logical.DeleteOperation, "credential/default", nil); err == nil || !strings.Contains(err.Error(), "widgetshop") {
		t.Fatalf("delete of a used credential: %v, want an error naming widgetshop", err)
	}

	if _, err := m.request(t, logical.DeleteOperation, "repo/widgetshop", nil); err != nil {
		t.Fatal(err)
	}

	if _, err := m.request(t, logical.DeleteOperation, "credential/default", nil); err != nil {
		t.Fatalf("delete of an unused credential: %v", err)
	}
}

func TestAccessPathsAnswerOnlyForConfiguredRepositories(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	m.mustWrite(t, "config", map[string]any{"proxy_url": "https://git-proxy.example.com"})
	m.mustWrite(t, "credential/default", map[string]any{"username": "x-access-token", "password": "ghp_secret"})
	m.mustWrite(t, "repo/widgetshop", map[string]any{"url": "https://github.com/acmecorp/widgetshop.git"})

	response, err := m.request(t, logical.ReadOperation, "access/widgetshop", nil)
	if err != nil || response.Data["clone_url"] != "https://git-proxy.example.com/widgetshop.git" {
		t.Fatalf("read access = %v, %v", response, err)
	}

	for key := range response.Data {
		if key != "name" && key != "clone_url" {
			t.Fatalf("access path returns %q; it must return nothing about the upstream", key)
		}
	}

	if response, err := m.request(t, logical.ReadOperation, "access/unknown", nil); err != nil || response != nil {
		t.Fatalf("read unknown access = %v, %v; want no response (404)", response, err)
	}

	// update is the push capability; holding it must not let a Requester write anything.
	if _, err := m.request(t, logical.UpdateOperation, "access/widgetshop", map[string]any{"url": "https://evil.example.com/x.git"}); err == nil {
		t.Fatal("write to an access path accepted")
	}
}

func TestConfigNeedsAnHTTPSProxyURL(t *testing.T) {
	t.Parallel()

	m := newTestMount(t)
	if _, err := m.request(t, logical.UpdateOperation, "config", map[string]any{"proxy_url": "http://git-proxy.example.com"}); err == nil {
		t.Fatal("http proxy_url accepted")
	}

	m.mustWrite(t, "config", map[string]any{"proxy_url": "https://git-proxy.example.com/"})
	response, err := m.request(t, logical.ReadOperation, "config", nil)
	if err != nil || response.Data["proxy_url"] != "https://git-proxy.example.com" {
		t.Fatalf("read config = %v, %v", response, err)
	}
}
