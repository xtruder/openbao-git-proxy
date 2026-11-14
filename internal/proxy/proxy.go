// Package proxy forwards git smart HTTP for Allowed Repositories to their
// upstream git hosts with each repository's credential, after OpenBao
// confirms that the Requester's own token holds a Grant on the repository's
// Access Path.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"slices"
	"time"

	"github.com/xtruder/openbao-git-proxy/internal/gitroute"
	"github.com/xtruder/openbao-git-proxy/internal/openbao"
	"github.com/xtruder/openbao-git-proxy/internal/repos"
)

// forwardedHeaders are the only request headers git needs upstream. Anything
// else, such as Authorization, Cookie or proxy headers, is dropped.
var forwardedHeaders = []string{
	"Accept", "Accept-Encoding", "Content-Encoding", "Content-Type", "Git-Protocol", "User-Agent",
}

var (
	errUpstreamCredential = errors.New("upstream refused the proxy's credential")
	errUpstreamRedirect   = errors.New("upstream redirected")
)

// CapabilityChecker reports the capabilities of a Requester's own token on
// a path. It returns openbao.ErrInvalidToken when OpenBao refuses the token.
type CapabilityChecker interface {
	CapabilitiesSelf(ctx context.Context, token, path string) ([]string, error)
}

// RepositoryLookup finds an Allowed Repository by name. It is consulted per
// request, so configuration refreshed from the mount applies at once.
type RepositoryLookup interface {
	Lookup(name string) (repos.Repository, bool)
}

// Options configures the proxy.
type Options struct {
	Repositories RepositoryLookup
	Transport    http.RoundTripper
	OpenBao      CapabilityChecker
	// AccessPrefix is the OpenBao path under which <name> is a repository's
	// Access Path, such as git-proxy/access.
	AccessPrefix string
	Logger       *slog.Logger
}

type proxy struct {
	options Options
	reverse *httputil.ReverseProxy
}

// New returns the proxy handler.
func New(options Options) http.Handler {
	p := &proxy{options: options}
	p.reverse = &httputil.ReverseProxy{
		Transport: options.Transport,
		// Flush at once: git shows the remote's progress as it arrives.
		FlushInterval:  -1,
		ModifyResponse: modifyResponse,
		ErrorHandler:   p.upstreamError,
		ErrorLog:       slog.NewLogLogger(options.Logger.Handler(), slog.LevelError),
	}
	return p
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	attributes := p.serve(recorder, r)
	attributes = append(attributes,
		slog.String("method", r.Method), slog.Int("status", recorder.status),
		slog.Duration("duration", time.Since(start)))
	p.options.Logger.LogAttrs(r.Context(), slog.LevelInfo, "request", attributes...)
}

// serve handles one request and returns attributes for its log line. The
// path is logged only once parsed, so the log holds only allowed shapes.
func (p *proxy) serve(w http.ResponseWriter, r *http.Request) []slog.Attr {
	route, err := gitroute.Parse(r.Method, r.URL)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}

	attributes := []slog.Attr{slog.String("repository", route.Name), slog.String("service", string(route.Service))}
	// Unconfigured repositories are refused before any credential is asked
	// for, so git doesn't file a Grant Request nobody could use.
	repository, ok := p.options.Repositories.Lookup(route.Name)
	if !ok {
		http.NotFound(w, r)
		return attributes
	}

	_, token, ok := r.BasicAuth()
	if !ok || !validToken(token) {
		challenge(w)
		return attributes
	}

	access := p.options.AccessPrefix + "/" + route.Name
	capability := route.Capability()
	capabilities, err := p.options.OpenBao.CapabilitiesSelf(r.Context(), token, access)
	switch {
	case errors.Is(err, openbao.ErrInvalidToken):
		challenge(w)
		return attributes
	case err != nil:
		p.options.Logger.ErrorContext(r.Context(), "check capabilities", "repository", route.Name, "error", err)
		http.Error(w, "OpenBao is unavailable", http.StatusServiceUnavailable)
		return attributes
	case !allows(capabilities, capability):
		item := access + "=" + capability
		http.Error(w, fmt.Sprintf("%s of %s needs a Grant of %s; request it with: bao-cred request --reason TEXT --item %s",
			route.Service, route.Name, item, item), http.StatusForbidden)
		return attributes
	}

	// Without full duplex, Go's HTTP/1 server drains the rest of the request
	// body before the first response byte, so a response that starts early,
	// such as an error during a push, would wait for the whole pack.
	if err := http.NewResponseController(w).EnableFullDuplex(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		p.options.Logger.WarnContext(r.Context(), "enable full duplex", "error", err)
	}

	// A copy per request binds the parsed route to its rewrite.
	reverse := *p.reverse
	reverse.Rewrite = func(request *httputil.ProxyRequest) { rewrite(request, route, repository) }
	reverse.ServeHTTP(w, r)
	return attributes
}

func rewrite(request *httputil.ProxyRequest, route gitroute.Route, repository repos.Repository) {
	request.Out.URL = route.UpstreamURL(repository.URL)
	request.Out.Host = ""

	header := http.Header{}
	for _, name := range forwardedHeaders {
		if values := request.In.Header.Values(name); len(values) > 0 {
			header[name] = slices.Clone(values)
		}
	}

	request.Out.Header = header
	request.Out.SetBasicAuth(repository.Username, repository.Password)
}

// allows reports whether capabilities include capability. update, the push
// capability, also allows fetching: every push reads the remote's refs, and
// a Requester trusted to push can be trusted to fetch.
func allows(capabilities []string, capability string) bool {
	return slices.Contains(capabilities, capability) || slices.Contains(capabilities, "root") ||
		(capability == "read" && slices.Contains(capabilities, "update"))
}

// modifyResponse keeps upstream cookies from clients and turns a refused
// upstream credential or a redirect into a gateway error: passing the
// host's challenge on would make git discard the Requester's valid token,
// and git following a redirect, such as for a renamed repository, would
// leave the proxy and its checks behind.
func modifyResponse(response *http.Response) error {
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusProxyAuthRequired:
		_ = response.Body.Close()
		return errUpstreamCredential
	case response.StatusCode >= 300 && response.StatusCode < 400:
		_ = response.Body.Close()
		return errUpstreamRedirect
	}

	response.Header.Del("Set-Cookie")
	return nil
}

func (p *proxy) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == nil {
		p.options.Logger.ErrorContext(r.Context(), "forward to upstream", "error", err)
	}

	http.Error(w, "upstream request failed", http.StatusBadGateway)
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="openbao-git-proxy", charset="UTF-8"`)
	http.Error(w, "use your OpenBao token as the password", http.StatusUnauthorized)
}

// validToken limits tokens to printable ASCII of a sane length before they
// reach OpenBao as a header.
func validToken(token string) bool {
	if token == "" || len(token) > 1024 {
		return false
	}

	for index := range len(token) {
		if token[index] <= ' ' || token[index] > '~' {
			return false
		}
	}

	return true
}

// statusRecorder keeps the response status for the log line. Unwrap lets
// the reverse proxy reach the underlying writer's Flush.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
