package proxy

import (
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitHTTPBackend serves bare repositories under root like GitHub does, for
// the PAT only.
func gitHTTPBackend(t *testing.T, root string) http.Handler {
	t.Helper()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: "/",
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, password, ok := r.BasicAuth(); !ok || password != pat {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		backend.ServeHTTP(w, r)
	})
}

func git(t *testing.T, directory string, arguments ...string) (string, error) {
	t.Helper()

	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Dir = directory
	// Keep the user's credential helpers and config out of the test.
	command.Env = append(os.Environ(), "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestRealGitFetchesAndPushesThroughTheProxy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bare := filepath.Join(root, "acmecorp", "widgetshop.git")
	h := newHarness(t, gitHTTPBackend(t, root))
	for _, arguments := range [][]string{
		{"init", "--bare", "--initial-branch=main", bare},
		{"-C", bare, "config", "http.receivepack", "true"},
	} {
		if output, err := git(t, root, arguments...); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}

	remote := func(token string) string {
		return strings.Replace(h.proxy.URL, "http://", "http://x-bao-token:"+token+"@", 1) + "/WidgetShop.git"
	}

	work := t.TempDir()
	if output, err := git(t, work, "clone", remote("pusher"), "clone"); err != nil {
		t.Fatalf("clone: %v\n%s", err, output)
	}

	checkout := filepath.Join(work, "clone")
	if output, err := git(t, checkout, "commit", "--allow-empty", "--no-gpg-sign", "-m", "first"); err != nil {
		t.Fatalf("commit: %v\n%s", err, output)
	}

	if output, err := git(t, checkout, "push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}

	if output, err := git(t, work, "clone", remote("reader"), "second"); err != nil || !strings.Contains(output, "Cloning") {
		t.Fatalf("clone as reader: %v\n%s", err, output)
	}

	output, err := git(t, filepath.Join(work, "second"), "push", remote("reader"), "HEAD:other")
	if err == nil || !strings.Contains(output, "git-proxy/access/widgetshop=update") {
		t.Fatalf("push as reader: %v, want refused naming the Grant\n%s", err, output)
	}

	if output, err := git(t, work, "ls-remote", remote("revoked")); err == nil {
		t.Fatalf("ls-remote with a revoked token succeeded\n%s", output)
	}
}
