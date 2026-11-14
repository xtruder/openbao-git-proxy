package repos

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// fakeMount answers like the git-proxy plugin: a missing path reads as nil.
type fakeMount struct {
	data    map[string]map[string]any
	listErr error
	readErr map[string]error
}

func (f *fakeMount) List(_ context.Context, path string) ([]string, error) {
	if path != "git-proxy/repos" {
		return nil, errors.New("unexpected list " + path)
	}

	if f.listErr != nil {
		return nil, f.listErr
	}

	var names []string
	for key := range f.data {
		if name, ok := strings.CutPrefix(key, "git-proxy/repo/"); ok {
			names = append(names, name)
		}
	}

	return names, nil
}

func (f *fakeMount) Read(_ context.Context, path string) (map[string]any, error) {
	if err := f.readErr[path]; err != nil {
		return nil, err
	}

	return f.data[path], nil
}

func newMount() *fakeMount {
	return &fakeMount{data: map[string]map[string]any{
		"git-proxy/credential/default": {"username": "x-access-token", "password": "ghp_secret"},
		"git-proxy/credential/gitlab":  {"username": "oauth2", "password": "glpat"},
		"git-proxy/repo/widgetshop":    {"url": "https://github.com/acmecorp/widgetshop.git", "credential": "default"},
		"git-proxy/repo/infra":         {"url": "https://gitlab.example.com/ops/infra.git", "credential": "gitlab"},
	}}
}

func TestLoadReadsRepositoriesWithTheirCredentials(t *testing.T) {
	t.Parallel()

	repositories := New(newMount(), "git-proxy", slog.New(slog.DiscardHandler))
	if err := repositories.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	widgetshop, ok := repositories.Lookup("widgetshop")
	if !ok || widgetshop.URL.String() != "https://github.com/acmecorp/widgetshop.git" ||
		widgetshop.Username != "x-access-token" || widgetshop.Password != "ghp_secret" {
		t.Fatalf("widgetshop = %#v, %v", widgetshop, ok)
	}

	if infra, ok := repositories.Lookup("infra"); !ok || infra.Username != "oauth2" || infra.Password != "glpat" {
		t.Fatalf("infra = %#v, %v", infra, ok)
	}

	if _, ok := repositories.Lookup("other"); ok {
		t.Fatal("unknown repository found")
	}
}

func TestLoadSkipsBrokenEntriesAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	mount := newMount()
	mount.data["git-proxy/repo/plain"] = map[string]any{"url": "http://github.com/a/b.git", "credential": "default"}
	mount.data["git-proxy/repo/orphan"] = map[string]any{"url": "https://github.com/a/b.git", "credential": "gone"}
	mount.data["git-proxy/repo/query"] = map[string]any{"url": "https://github.com/a/b.git?x=1", "credential": "default"}
	mount.data["git-proxy/repo/badcredential"] = map[string]any{"url": "https://github.com/a/b.git", "credential": "../repo/x"}
	logs := &bytes.Buffer{}
	repositories := New(mount, "git-proxy", slog.New(slog.NewTextHandler(logs, nil)))
	if err := repositories.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"plain", "orphan", "query", "badcredential"} {
		if _, ok := repositories.Lookup(name); ok {
			t.Fatalf("broken repository %s loaded", name)
		}

		if !strings.Contains(logs.String(), name) {
			t.Fatalf("logs don't name %s: %s", name, logs)
		}
	}

	if _, ok := repositories.Lookup("widgetshop"); !ok {
		t.Fatal("a broken entry dropped the good ones")
	}

	if strings.Contains(logs.String(), "ghp_secret") {
		t.Fatalf("logs contain a password: %s", logs)
	}
}

func TestAFailedLoadKeepsTheLastConfiguration(t *testing.T) {
	t.Parallel()

	mount := newMount()
	repositories := New(mount, "git-proxy", slog.New(slog.DiscardHandler))
	if err := repositories.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	mount.listErr = errors.New("OpenBao sealed")
	if err := repositories.Load(t.Context()); err == nil {
		t.Fatal("Load succeeded while OpenBao is unavailable")
	}

	if _, ok := repositories.Lookup("widgetshop"); !ok {
		t.Fatal("a failed refresh dropped the last configuration")
	}

	delete(mount.data, "git-proxy/repo/widgetshop")
	mount.listErr = nil
	if err := repositories.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, ok := repositories.Lookup("widgetshop"); ok {
		t.Fatal("a removed repository is still served")
	}
}

func TestAFailedReadKeepsTheLastConfiguration(t *testing.T) {
	t.Parallel()

	mount := newMount()
	repositories := New(mount, "git-proxy", slog.New(slog.DiscardHandler))
	if err := repositories.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	// OpenBao failing mid-reload is not a broken entry: nothing is dropped.
	for _, path := range []string{"git-proxy/repo/infra", "git-proxy/credential/gitlab"} {
		mount.readErr = map[string]error{path: errors.New("connection reset")}
		if err := repositories.Load(t.Context()); err == nil {
			t.Fatalf("Load succeeded while reading %s failed", path)
		}

		if _, ok := repositories.Lookup("infra"); !ok {
			t.Fatalf("a failed read of %s dropped infra", path)
		}
	}
}
