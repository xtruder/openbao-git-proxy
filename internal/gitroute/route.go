// Package gitroute recognises the git smart HTTP requests the proxy forwards
// and refuses everything else.
package gitroute

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Service is a git smart HTTP service.
type Service string

const (
	// UploadPack serves fetches and clones.
	UploadPack Service = "git-upload-pack"
	// ReceivePack serves pushes.
	ReceivePack Service = "git-receive-pack"
)

// ErrNotGitRoute is returned for any request that isn't git smart HTTP for
// one repository.
var ErrNotGitRoute = errors.New("not a git smart HTTP request")

// Route is one git smart HTTP request for a repository. Owner and Repo are
// lowercased and Repo has no .git suffix, because GitHub treats names case
// insensitively and one repository must map to one marker path.
type Route struct {
	Owner   string
	Repo    string
	Service Service
	// Advertisement is the GET info/refs request that starts every fetch and
	// push; otherwise the route is the POST that carries the pack.
	Advertisement bool
}

// Parse accepts only these requests, with or without .git:
//
//	GET  /<owner>/<repo>.git/info/refs?service=git-upload-pack|git-receive-pack
//	POST /<owner>/<repo>.git/git-upload-pack
//	POST /<owner>/<repo>.git/git-receive-pack
//
// Dumb HTTP, other services, other query parameters and any escaped
// character in the path are refused, so nothing reaches upstream that the
// allow-list didn't see in plain form.
func Parse(method string, target *url.URL) (Route, error) {
	if target.RawPath != "" || target.Opaque != "" {
		return Route{}, ErrNotGitRoute
	}

	segments := strings.Split(target.Path, "/")
	if len(segments) < 4 || segments[0] != "" {
		return Route{}, ErrNotGitRoute
	}

	owner, repo := strings.ToLower(segments[1]), strings.ToLower(strings.TrimSuffix(segments[2], ".git"))
	if !ValidName(owner) || !ValidName(repo) {
		return Route{}, ErrNotGitRoute
	}

	route := Route{Owner: owner, Repo: repo}
	rest := segments[3:]
	switch {
	case method == http.MethodGet && len(rest) == 2 && rest[0] == "info" && rest[1] == "refs":
		query, err := url.ParseQuery(target.RawQuery)
		if err != nil || len(query) != 1 || len(query["service"]) != 1 {
			return Route{}, ErrNotGitRoute
		}

		route.Service = Service(query.Get("service"))
		route.Advertisement = true
	case method == http.MethodPost && len(rest) == 1:
		route.Service = Service(rest[0])
	default:
		return Route{}, ErrNotGitRoute
	}

	if route.Service != UploadPack && route.Service != ReceivePack {
		return Route{}, ErrNotGitRoute
	}

	return route, nil
}

// Repository is the route's lowercased owner/repo.
func (r Route) Repository() string {
	return r.Owner + "/" + r.Repo
}

// Capability is what a Requester needs on the repository's Marker Path:
// update to push, read to fetch.
func (r Route) Capability() string {
	if r.Service == ReceivePack {
		return "update"
	}

	return "read"
}

// UpstreamPath is the canonical path and query of the route on GitHub.
func (r Route) UpstreamPath() string {
	base := "/" + r.Owner + "/" + r.Repo + ".git/"
	if r.Advertisement {
		return base + "info/refs?service=" + string(r.Service)
	}

	return base + string(r.Service)
}

// ValidName reports whether value is a lowercased owner or repository name.
// It allows the characters GitHub allows in owner and repository
// names, which also keeps them safe as OpenBao path segments.
func ValidName(value string) bool {
	return value != "" && value != "." && value != ".." &&
		strings.Trim(value, "abcdefghijklmnopqrstuvwxyz0123456789-_.") == ""
}
