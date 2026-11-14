package gitroute

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

func TestParseAcceptsSmartHTTPByRepositoryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, target string
		want           Route
	}{
		{http.MethodGet, "/widgetshop.git/info/refs?service=git-upload-pack", Route{Name: "widgetshop", Service: UploadPack, Advertisement: true}},
		{http.MethodGet, "/WidgetShop/info/refs?service=git-receive-pack", Route{Name: "widgetshop", Service: ReceivePack, Advertisement: true}},
		{http.MethodPost, "/widgetshop.git/git-upload-pack", Route{Name: "widgetshop", Service: UploadPack}},
		{http.MethodPost, "/my_repo.v2/git-receive-pack", Route{Name: "my_repo.v2", Service: ReceivePack}},
	}
	for _, test := range tests {
		route, err := Parse(test.method, mustParseURL(t, test.target))
		if err != nil || route != test.want {
			t.Fatalf("Parse(%s %s) = %#v, %v; want %#v", test.method, test.target, route, err, test.want)
		}
	}
}

func TestParseRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ method, target string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/widgetshop"},
		{http.MethodGet, "/widgetshop.git/info/refs"},                            // dumb HTTP
		{http.MethodGet, "/widgetshop.git/info/refs?service=git-upload-archive"}, // not served
		{http.MethodGet, "/widgetshop.git/info/refs?service=git-upload-pack&service=git-receive-pack"},
		{http.MethodGet, "/widgetshop.git/info/refs?service=git-upload-pack&x=1"},
		{http.MethodPost, "/widgetshop.git/info/refs?service=git-upload-pack"},
		{http.MethodGet, "/widgetshop.git/git-upload-pack"},
		{http.MethodPut, "/widgetshop.git/git-receive-pack"},
		{http.MethodGet, "/widgetshop.git/HEAD"},
		{http.MethodPost, "/widgetshop.git/info/lfs/objects/batch"},
		{http.MethodPost, "/acmecorp/widgetshop.git/git-upload-pack"}, // upstream paths are never parsed
		{http.MethodPost, "//widgetshop.git/git-upload-pack"},
		{http.MethodPost, "/widgetshop.git/git-upload-pack/"},
		{http.MethodPost, "/../git-upload-pack"},
		{http.MethodPost, "/.git/git-upload-pack"},
		{http.MethodPost, "/auth.git/git-upload-pack"}, // never grantable
		{http.MethodPost, "/widget%73hop.git/git-upload-pack"},
		{http.MethodPost, "/widget@shop.git/git-upload-pack"},
	} {
		if route, err := Parse(test.method, mustParseURL(t, test.target)); !errors.Is(err, ErrNotGitRoute) {
			t.Fatalf("Parse(%s %s) = %#v, %v; want ErrNotGitRoute", test.method, test.target, route, err)
		}
	}
}

func TestUpstreamURLAppendsTheServiceToTheConfiguredURL(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		route    Route
		upstream string
		want     string
	}{
		{Route{Name: "w", Service: UploadPack, Advertisement: true}, "https://github.com/acmecorp/widgetshop.git",
			"https://github.com/acmecorp/widgetshop.git/info/refs?service=git-upload-pack"},
		{Route{Name: "i", Service: ReceivePack}, "https://gitlab.example.com/ops/platform/infra.git/",
			"https://gitlab.example.com/ops/platform/infra.git/git-receive-pack"},
		{Route{Name: "a", Service: UploadPack}, "https://dev.azure.com/org/project/_git/repo",
			"https://dev.azure.com/org/project/_git/repo/git-upload-pack"},
	} {
		if got := test.route.UpstreamURL(mustParseURL(t, test.upstream)).String(); got != test.want {
			t.Fatalf("UpstreamURL(%s) = %s, want %s", test.upstream, got, test.want)
		}
	}
}

func TestValidUpstreamURL(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]bool{
		"https://github.com/acmecorp/widgetshop.git":  true,
		"https://dev.azure.com/org/project/_git/repo": true,
		"http://github.com/a/b.git":                   false,
		"https:///a/b.git":                            false,
		"https://user:pw@github.com/a/b.git":          false,
		"https://github.com/a/b.git?x=1":              false,
		"https://github.com/a/b.git?":                 false,
		"https://github.com/a/b.git#frag":             false,
		"github.com/a/b.git":                          false,
	} {
		if err := ValidUpstreamURL(raw); (err == nil) != want {
			t.Fatalf("ValidUpstreamURL(%q) = %v, want valid=%v", raw, err, want)
		}
	}
}

func TestPushesNeedUpdateAndFetchesRead(t *testing.T) {
	t.Parallel()

	if got := (Route{Service: UploadPack, Advertisement: true}).Capability(); got != "read" {
		t.Fatalf("fetch needs %q, want read", got)
	}

	if got := (Route{Service: ReceivePack}).Capability(); got != "update" {
		t.Fatalf("push needs %q, want update", got)
	}
}

func TestValidRepositoryName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"widgetshop": true, "my_repo.v2": true, "0day": true,
		"": false, "Widgetshop": false, "-x": false, ".x": false, "x.git": false,
		"auth": false, "sys": false, "a/b": false, "a b": false,
	} {
		if got := ValidRepositoryName(name); got != want {
			t.Fatalf("ValidRepositoryName(%q) = %v, want %v", name, got, want)
		}
	}
}
