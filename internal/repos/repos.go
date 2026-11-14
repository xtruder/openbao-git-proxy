// Package repos holds the proxy's view of the git-proxy mount: the Allowed
// Repositories, each with its upstream URL and credential, read with the
// proxy's own token and refreshed so changes apply without a restart.
package repos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync/atomic"

	"github.com/xtruder/openbao-git-proxy/internal/gitroute"
)

// Reader reads the mount. Read returns nil data for a missing path.
type Reader interface {
	List(ctx context.Context, path string) ([]string, error)
	Read(ctx context.Context, path string) (map[string]any, error)
}

// Repository is where the proxy forwards one repository and how it
// authenticates there. Username and Password never reach clients.
type Repository struct {
	URL      *url.URL
	Username string
	Password string
}

// Repositories is the last configuration loaded from the mount.
type Repositories struct {
	reader  Reader
	mount   string
	logger  *slog.Logger
	current atomic.Pointer[map[string]Repository]
}

// New returns an empty set; Load fills it.
func New(reader Reader, mount string, logger *slog.Logger) *Repositories {
	r := &Repositories{reader: reader, mount: mount, logger: logger}
	r.current.Store(&map[string]Repository{})
	return r
}

// Lookup returns the repository named name.
func (r *Repositories) Lookup(name string) (Repository, bool) {
	repository, ok := (*r.current.Load())[name]
	return repository, ok
}

// errBroken marks an entry that is missing or invalid, as opposed to one
// that couldn't be read.
var errBroken = errors.New("broken entry")

// Load reads the whole configuration and replaces the current one. If
// OpenBao fails on any read, the last configuration stays in use: OpenBao
// being briefly unavailable shouldn't stop git. A missing or invalid entry
// is logged and skipped, so one mistake doesn't take every repository down.
func (r *Repositories) Load(ctx context.Context) error {
	names, err := r.reader.List(ctx, r.mount+"/repos")
	if err != nil {
		return fmt.Errorf("list repositories: %w", err)
	}

	if len(names) == 0 {
		r.logger.WarnContext(ctx, "no repositories configured; check the mount path", "mount", r.mount)
	}

	loaded := make(map[string]Repository, len(names))
	credentials := map[string]map[string]any{}
	for _, name := range names {
		repository, err := r.load(ctx, name, credentials)
		if errors.Is(err, errBroken) {
			r.logger.ErrorContext(ctx, "skip repository", "repository", name, "error", err)
			continue
		}

		if err != nil {
			return fmt.Errorf("load repository %s: %w", name, err)
		}

		loaded[name] = repository
	}

	r.current.Store(&loaded)
	return nil
}

func (r *Repositories) load(ctx context.Context, name string, credentials map[string]map[string]any) (Repository, error) {
	if !gitroute.ValidRepositoryName(name) {
		return Repository{}, fmt.Errorf("%w: invalid name", errBroken)
	}

	entry, err := r.reader.Read(ctx, r.mount+"/repo/"+name)
	if err != nil {
		return Repository{}, err
	}

	rawURL, _ := entry["url"].(string)
	credentialName, _ := entry["credential"].(string)
	// The plugin validates the URL when it is written; checking again keeps
	// the credential from ever going out over plain HTTP. The error doesn't
	// quote the URL, which could carry a secret if it were ever stored.
	if urlErr := gitroute.ValidUpstreamURL(rawURL); urlErr != nil {
		return Repository{}, fmt.Errorf("%w: %w", errBroken, urlErr)
	}

	clone, _ := url.Parse(rawURL)
	if !gitroute.ValidName(credentialName) {
		return Repository{}, fmt.Errorf("%w: invalid credential name", errBroken)
	}

	credential, ok := credentials[credentialName]
	if !ok {
		if credential, err = r.reader.Read(ctx, r.mount+"/credential/"+credentialName); err != nil {
			return Repository{}, fmt.Errorf("read credential %q: %w", credentialName, err)
		}

		credentials[credentialName] = credential
	}

	username, _ := credential["username"].(string)
	password, _ := credential["password"].(string)
	if username == "" || password == "" {
		return Repository{}, fmt.Errorf("%w: credential %q is missing or incomplete", errBroken, credentialName)
	}

	return Repository{URL: clone, Username: username, Password: password}, nil
}
