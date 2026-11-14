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

// Route is one git smart HTTP request for a repository, addressed by its
// lowercased Repository Name.
type Route struct {
	Name    string
	Service Service
	// Advertisement is the GET info/refs request that starts every fetch and
	// push; otherwise the route is the POST that carries the pack.
	Advertisement bool
}

// Parse accepts only these requests, with or without .git:
//
//	GET  /<name>.git/info/refs?service=git-upload-pack|git-receive-pack
//	POST /<name>.git/git-upload-pack
//	POST /<name>.git/git-receive-pack
//
// The repository is named, never addressed by its upstream path, so the
// request has one segment to validate whatever the host's path layout.
// Dumb HTTP, other services, other query parameters and any escaped
// character in the path are refused.
func Parse(method string, target *url.URL) (Route, error) {
	if target.RawPath != "" || target.Opaque != "" {
		return Route{}, ErrNotGitRoute
	}

	segments := strings.Split(target.Path, "/")
	if len(segments) < 3 || segments[0] != "" {
		return Route{}, ErrNotGitRoute
	}

	route := Route{Name: strings.ToLower(strings.TrimSuffix(segments[1], ".git"))}
	if !ValidRepositoryName(route.Name) {
		return Route{}, ErrNotGitRoute
	}

	rest := segments[2:]
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

// Capability is what a Requester needs on the repository's Access Path:
// update to push, read to fetch.
func (r Route) Capability() string {
	if r.Service == ReceivePack {
		return "update"
	}

	return "read"
}

// UpstreamURL appends the route to the repository's configured clone URL.
func (r Route) UpstreamURL(clone *url.URL) *url.URL {
	upstream := *clone
	upstream.Path = strings.TrimSuffix(clone.Path, "/")
	upstream.RawPath = ""
	upstream.RawQuery = ""
	upstream.Fragment = ""
	if r.Advertisement {
		upstream.Path += "/info/refs"
		upstream.RawQuery = "service=" + string(r.Service)
	} else {
		upstream.Path += "/" + string(r.Service)
	}

	return &upstream
}
