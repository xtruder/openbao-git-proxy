package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtruder/openbao-git-proxy/internal/openbao"
)

const pat = "ghp_classic-token"

// fakeOpenBao answers capabilities-self from a table of token → path →
// capabilities; tokens missing from the table are invalid.
type fakeOpenBao struct {
	mu           sync.Mutex
	capabilities map[string]map[string][]string
	err          error
	calls        []string
}

func (f *fakeOpenBao) CapabilitiesSelf(_ context.Context, token, path string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if f.err != nil {
		return nil, f.err
	}

	paths, ok := f.capabilities[token]
	if !ok {
		return nil, openbao.ErrInvalidToken
	}

	if capabilities, ok := paths[path]; ok {
		return capabilities, nil
	}

	return []string{"deny"}, nil
}

func (f *fakeOpenBao) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type harness struct {
	proxy   *httptest.Server
	openbao *fakeOpenBao
	logs    *bytes.Buffer

	mu       sync.Mutex
	upstream *http.Request
}

// lastUpstream returns the request the default upstream last received and
// forgets it.
func (h *harness) lastUpstream() *http.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	request := h.upstream
	h.upstream = nil
	return request
}

// newHarness starts the proxy in front of upstream; a nil upstream answers
// every request with 200 and records it.
func newHarness(t *testing.T, upstream http.Handler) *harness {
	t.Helper()

	h := &harness{
		openbao: &fakeOpenBao{capabilities: map[string]map[string][]string{
			"reader": {"git-proxy/acmecorp/widgetshop": {"read"}},
			"pusher": {"git-proxy/acmecorp/widgetshop": {"read", "update"}},
			"root":   {"git-proxy/acmecorp/widgetshop": {"root"}},
		}},
		logs: &bytes.Buffer{},
	}
	if upstream == nil {
		upstream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.mu.Lock()
			h.upstream = r.Clone(context.WithoutCancel(r.Context()))
			h.mu.Unlock()
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			w.Header().Set("Set-Cookie", "logged_in=yes")
			_, _ = io.WriteString(w, "001e# service=git-upload-pack\n")
		})
	}

	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)
	upstreamURL, err := url.Parse(upstreamServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	h.proxy = httptest.NewServer(New(Options{
		Upstream:     upstreamURL,
		Transport:    upstreamServer.Client().Transport,
		OpenBao:      h.openbao,
		Credential:   func() string { return pat },
		MarkerPrefix: "git-proxy",
		Repositories: []string{"acmecorp/widgetshop"},
		Logger:       slog.New(slog.NewTextHandler(h.logs, nil)),
	}))
	t.Cleanup(h.proxy.Close)
	return h
}

// response is a finished response with its body read.
type response struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func (h *harness) do(t *testing.T, method, target, token string, body io.Reader) response {
	t.Helper()

	streamed := h.send(t, method, target, token, body)
	defer func() { _ = streamed.Body.Close() }()
	contents, err := io.ReadAll(streamed.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response{StatusCode: streamed.StatusCode, Header: streamed.Header, Body: string(contents)}
}

// send returns the response as soon as its headers arrive; the caller closes
// the body.
func (h *harness) send(t *testing.T, method, target, token string, body io.Reader) *http.Response {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), method, h.proxy.URL+target, body)
	if err != nil {
		t.Fatal(err)
	}

	if token != "" {
		request.SetBasicAuth("x-bao-token", token)
	}

	request.Header.Set("Git-Protocol", "version=2")
	request.Header.Set("Cookie", "client=secret")
	streamed, err := h.proxy.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}

	return streamed
}

const (
	fetchRefs = "/acmecorp/widgetshop.git/info/refs?service=git-upload-pack"
	pushRefs  = "/AcmeCorp/WidgetShop/info/refs?service=git-receive-pack"
	fetchPack = "/acmecorp/widgetshop.git/git-upload-pack"
	pushPack  = "/acmecorp/widgetshop.git/git-receive-pack"
)

func TestRefusesOtherRequestsWithoutAskingForCredentials(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	for _, target := range []string{
		"/acmecorp/widgetshop.git/HEAD",
		"/repos/acmecorp/widgetshop",
		"/graphql",
		"/acmecorp/other.git/info/refs?service=git-upload-pack", // not configured
	} {
		response := h.do(t, http.MethodGet, target, "pusher", nil)
		if response.StatusCode != http.StatusNotFound || response.Header.Get("WWW-Authenticate") != "" {
			t.Fatalf("GET %s = %d %q, want 404 without a challenge", target, response.StatusCode, response.Header.Get("WWW-Authenticate"))
		}
	}

	if upstream, calls := h.lastUpstream(), h.openbao.callCount(); upstream != nil || calls != 0 {
		t.Fatalf("upstream %v, %d OpenBao calls; want none", upstream, calls)
	}
}

func TestChallengesRequestsWithoutAValidToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	for _, token := range []string{"", "revoked"} {
		response := h.do(t, http.MethodGet, fetchRefs, token, nil)
		if response.StatusCode != http.StatusUnauthorized ||
			!strings.HasPrefix(response.Header.Get("WWW-Authenticate"), "Basic ") {
			t.Fatalf("token %q: status %d, challenge %q; want 401 Basic", token, response.StatusCode, response.Header.Get("WWW-Authenticate"))
		}
	}

	if h.lastUpstream() != nil {
		t.Fatal("request without a valid token reached upstream")
	}
}

func TestRefusesRequestersWithoutAGrantOnTheMarkerPath(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	h.openbao.capabilities["stranger"] = map[string][]string{}
	for _, test := range []struct{ method, target, token, capability string }{
		{http.MethodGet, fetchRefs, "stranger", "read"},
		{http.MethodPost, fetchPack, "stranger", "read"},
		{http.MethodGet, pushRefs, "reader", "update"},
		{http.MethodPost, pushPack, "reader", "update"},
	} {
		response := h.do(t, test.method, test.target, test.token, strings.NewReader("0000"))
		body := response.Body
		if response.StatusCode != http.StatusForbidden || response.Header.Get("WWW-Authenticate") != "" {
			t.Fatalf("%s %s as %s = %d, want 403 without a challenge", test.method, test.target, test.token, response.StatusCode)
		}

		// git shows a text/plain error body to the user.
		want := "git-proxy/acmecorp/widgetshop=" + test.capability
		if !strings.Contains(body, want) || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") {
			t.Fatalf("403 body %q (%s) doesn't name %s", body, response.Header.Get("Content-Type"), want)
		}
	}

	if h.lastUpstream() != nil {
		t.Fatal("refused request reached upstream")
	}
}

func TestForwardsGrantedRequestsWithThePATOnly(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	for _, test := range []struct{ method, target, token, upstream string }{
		{http.MethodGet, fetchRefs, "reader", "/acmecorp/widgetshop.git/info/refs?service=git-upload-pack"},
		{http.MethodPost, fetchPack, "reader", "/acmecorp/widgetshop.git/git-upload-pack"},
		{http.MethodGet, pushRefs, "pusher", "/acmecorp/widgetshop.git/info/refs?service=git-receive-pack"},
		{http.MethodPost, pushPack, "root", "/acmecorp/widgetshop.git/git-receive-pack"},
	} {
		response := h.do(t, test.method, test.target, test.token, strings.NewReader("0000"))
		body := response.Body
		if response.StatusCode != http.StatusOK || body != "001e# service=git-upload-pack\n" {
			t.Fatalf("%s %s = %d %q", test.method, test.target, response.StatusCode, body)
		}

		if response.Header.Get("Set-Cookie") != "" {
			t.Fatal("upstream cookie reached the client")
		}

		got := h.lastUpstream()
		if got == nil || got.Method != test.method || got.URL.RequestURI() != test.upstream {
			t.Fatalf("upstream request = %v, want %s %s", got, test.method, test.upstream)
		}

		user, password, ok := got.BasicAuth()
		if !ok || user != "x-access-token" || password != pat {
			t.Fatalf("upstream Authorization = %q", got.Header.Get("Authorization"))
		}

		if got.Header.Get("Cookie") != "" || got.Header.Get("Git-Protocol") != "version=2" {
			t.Fatalf("upstream headers = %v; want Git-Protocol kept and Cookie dropped", got.Header)
		}
	}
}

func TestUpstreamRejectingThePATIsABadGateway(t *testing.T) {
	t.Parallel()

	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	response := h.do(t, http.MethodGet, fetchRefs, "reader", nil)
	// GitHub's challenge would make git discard the Requester's valid token.
	if response.StatusCode != http.StatusBadGateway || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("status %d, challenge %q; want 502 without a challenge", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
}

func TestUpstreamRedirectsDontReachTheClient(t *testing.T) {
	t.Parallel()

	// GitHub redirects renamed repositories; git following that would bypass
	// the allow-list and the Grant check.
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/acmecorp/renamed.git/info/refs?service=git-upload-pack", http.StatusMovedPermanently)
	}))
	response := h.do(t, http.MethodGet, fetchRefs, "reader", nil)
	if response.StatusCode != http.StatusBadGateway || response.Header.Get("Location") != "" {
		t.Fatalf("status %d, Location %q; want 502 without a redirect", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestOpenBaoUnavailableIsServiceUnavailable(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	h.openbao.err = errors.New("connection refused")
	if response := h.do(t, http.MethodGet, fetchRefs, "reader", nil); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", response.StatusCode)
	}
}

func TestStreamsBothDirections(t *testing.T) {
	t.Parallel()

	firstRequestChunk := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Like GitHub over HTTP/2, the fake upstream answers while still reading.
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Errorf("enable full duplex: %v", err)
		}

		chunk := make([]byte, 4)
		if _, err := io.ReadFull(r.Body, chunk); err != nil {
			t.Errorf("read first request chunk: %v", err)
			return
		}

		close(firstRequestChunk)
		_, _ = io.WriteString(w, "progress")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "done")
	}))

	pipeReader, pipeWriter := io.Pipe()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.proxy.URL+fetchPack, pipeReader)
	if err != nil {
		t.Fatal(err)
	}

	request.SetBasicAuth("x-bao-token", "reader")
	responses := make(chan *http.Response, 1)
	go func() {
		// Do returns once the headers arrive, while the body is still being sent.
		streamed, err := h.proxy.Client().Do(request) //nolint:bodyclose // the test closes it after receiving it
		if err != nil {
			t.Errorf("send request: %v", err)
		}

		responses <- streamed
	}()

	_, _ = io.WriteString(pipeWriter, "0032")
	select {
	case <-firstRequestChunk:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream didn't get the request body before the client finished sending it")
	}

	var streamed *http.Response
	select {
	case streamed = <-responses:
	case <-time.After(5 * time.Second):
		t.Fatal("no response headers before upstream finished")
	}

	if streamed == nil {
		t.FailNow()
	}

	defer func() { _ = streamed.Body.Close() }()

	chunk := make([]byte, len("progress"))
	if _, err := io.ReadFull(streamed.Body, chunk); err != nil || string(chunk) != "progress" {
		t.Fatalf("first response chunk %q, %v", chunk, err)
	}

	close(release)
	_ = pipeWriter.Close()
	rest, _ := io.ReadAll(streamed.Body)
	if string(rest) != "done" {
		t.Fatalf("rest of response %q", rest)
	}
}

func TestLogsNoCredentials(t *testing.T) {
	t.Parallel()

	h := newHarness(t, nil)
	h.do(t, http.MethodGet, fetchRefs, "reader", nil)
	h.do(t, http.MethodGet, pushRefs, "reader", nil)
	h.do(t, http.MethodGet, fetchRefs, "revoked", nil)
	logs := h.logs.String()
	if !strings.Contains(logs, "acmecorp/widgetshop") {
		t.Fatalf("logs don't record the repository: %s", logs)
	}

	for _, secret := range []string{"reader", "revoked", pat, base64.StdEncoding.EncodeToString([]byte("x-bao-token:reader"))} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain %q: %s", secret, logs)
		}
	}
}
