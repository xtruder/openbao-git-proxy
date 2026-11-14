package gitroute

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func TestParseAcceptsSmartHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, target string
		want           Route
		upstream       string
	}{
		{
			http.MethodGet, "/acmecorp/widgetshop.git/info/refs?service=git-upload-pack",
			Route{Owner: "acmecorp", Repo: "widgetshop", Service: UploadPack, Advertisement: true},
			"/acmecorp/widgetshop.git/info/refs?service=git-upload-pack",
		},
		{
			http.MethodGet, "/AcmeCorp/WidgetShop/info/refs?service=git-receive-pack",
			Route{Owner: "acmecorp", Repo: "widgetshop", Service: ReceivePack, Advertisement: true},
			"/acmecorp/widgetshop.git/info/refs?service=git-receive-pack",
		},
		{
			http.MethodPost, "/acmecorp/widgetshop.git/git-upload-pack",
			Route{Owner: "acmecorp", Repo: "widgetshop", Service: UploadPack},
			"/acmecorp/widgetshop.git/git-upload-pack",
		},
		{
			http.MethodPost, "/xtruder/my_repo.v2/git-receive-pack",
			Route{Owner: "xtruder", Repo: "my_repo.v2", Service: ReceivePack},
			"/xtruder/my_repo.v2.git/git-receive-pack",
		},
	}
	for _, test := range tests {
		target, err := url.Parse(test.target)
		if err != nil {
			t.Fatal(err)
		}

		route, err := Parse(test.method, target)
		if err != nil {
			t.Fatalf("Parse(%s %s): %v", test.method, test.target, err)
		}

		if route != test.want {
			t.Fatalf("Parse(%s %s) = %#v, want %#v", test.method, test.target, route, test.want)
		}

		if got := route.UpstreamPath(); got != test.upstream {
			t.Fatalf("UpstreamPath of %s = %q, want %q", test.target, got, test.upstream)
		}
	}
}

func TestParseRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ method, target string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/acmecorp/widgetshop"},
		{http.MethodGet, "/acmecorp/widgetshop.git/info/refs"},                            // dumb HTTP
		{http.MethodGet, "/acmecorp/widgetshop.git/info/refs?service=git-upload-archive"}, // not served
		{http.MethodGet, "/acmecorp/widgetshop.git/info/refs?service=git-upload-pack&service=git-receive-pack"},
		{http.MethodPost, "/acmecorp/widgetshop.git/info/refs?service=git-upload-pack"},
		{http.MethodGet, "/acmecorp/widgetshop.git/git-upload-pack"},
		{http.MethodPut, "/acmecorp/widgetshop.git/git-receive-pack"},
		{http.MethodGet, "/acmecorp/widgetshop.git/HEAD"},
		{http.MethodGet, "/acmecorp/widgetshop.git/objects/info/packs"},
		{http.MethodGet, "/repos/acmecorp/widgetshop/info/refs?service=git-upload-pack"},
		{http.MethodPost, "/graphql"},
		{http.MethodPost, "/acmecorp//widgetshop.git/git-upload-pack"},
		{http.MethodPost, "//acmecorp/widgetshop.git/git-upload-pack"},
		{http.MethodPost, "/acmecorp/widgetshop.git/git-upload-pack/"},
		{http.MethodPost, "/acmecorp/../widgetshop.git/git-upload-pack"},
		{http.MethodPost, "/acmecorp/..git/git-upload-pack"},
		{http.MethodPost, "/acmecorp/.git/git-upload-pack"},
		{http.MethodPost, "/acmecorp%2Fwidgetshop/x.git/git-upload-pack"}, // encoded slash
		{http.MethodPost, "/acmecor%70/widgetshop.git/git-upload-pack"},   // any encoding
		{http.MethodPost, "/acme corp/widgetshop.git/git-upload-pack"},
		{http.MethodPost, "/acmecorp/widget@shop.git/git-upload-pack"},
	} {
		target, err := url.Parse(test.target)
		if err != nil {
			t.Fatal(err)
		}

		if route, err := Parse(test.method, target); !errors.Is(err, ErrNotGitRoute) {
			t.Fatalf("Parse(%s %s) = %#v, %v; want ErrNotGitRoute", test.method, test.target, route, err)
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
