// Package credential holds the upstream credential, read from OpenBao with
// the proxy's own token and never handed to clients.
package credential

import (
	"context"
	"sync/atomic"
)

// Reader reads one string field of an OpenBao secret.
type Reader interface {
	ReadField(ctx context.Context, path, field string) (string, error)
}

// Credential is the current upstream token.
type Credential struct {
	reader      Reader
	path, field string
	token       atomic.Pointer[string]
}

// Load reads the credential once; the proxy can't serve without it.
func Load(ctx context.Context, reader Reader, path, field string) (*Credential, error) {
	credential := &Credential{reader: reader, path: path, field: field}
	if err := credential.Refresh(ctx); err != nil {
		return nil, err
	}

	return credential, nil
}

// Token returns the current upstream token.
func (c *Credential) Token() string {
	return *c.token.Load()
}

// Refresh reads the credential again, so a rotated token applies without a
// restart. On failure the last good token stays in use: GitHub, not
// OpenBao's availability, decides whether it still works.
func (c *Credential) Refresh(ctx context.Context) error {
	token, err := c.reader.ReadField(ctx, c.path, c.field)
	if err != nil {
		return err
	}

	c.token.Store(&token)
	return nil
}
