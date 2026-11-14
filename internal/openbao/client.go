// Package openbao is the narrow OpenBao API surface the proxy uses.
package openbao

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	openbao "github.com/openbao/openbao/api/v2"
)

// ErrInvalidToken means OpenBao refused a Requester's token itself, such as
// an expired, revoked or malformed one.
var ErrInvalidToken = errors.New("invalid OpenBao token")

// Config configures the client.
type Config struct {
	Address   string
	Namespace string
	CAFile    string
	// Token is the proxy's own credential. It reads the upstream credential;
	// Requester tokens are used only for capabilities-self.
	Token string
}

// Client calls OpenBao with the proxy's token or a Requester's.
type Client struct {
	api       *openbao.Client
	namespace string
}

// New constructs a client.
func New(config Config) (*Client, error) {
	apiConfig := openbao.DefaultConfig()
	if apiConfig.Error != nil {
		return nil, fmt.Errorf("configure OpenBao client: %w", apiConfig.Error)
	}

	apiConfig.Address = config.Address
	if config.CAFile != "" {
		if err := apiConfig.ConfigureTLS(&openbao.TLSConfig{CACert: config.CAFile}); err != nil {
			return nil, fmt.Errorf("configure OpenBao TLS: %w", err)
		}
	}

	api, err := openbao.NewClient(apiConfig)
	if err != nil {
		return nil, fmt.Errorf("create OpenBao client: %w", err)
	}

	// NewClient picks up BAO_TOKEN and BAO_NAMESPACE from the environment;
	// only the configuration counts.
	api.SetToken(config.Token)
	api.SetNamespace(config.Namespace)
	return &Client{api: api, namespace: config.Namespace}, nil
}

// CapabilitiesSelf returns the capabilities of token on path, including
// those its identity inherits, so Grants of every scope count.
func (c *Client) CapabilitiesSelf(ctx context.Context, token, path string) ([]string, error) {
	api, err := c.api.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone OpenBao client: %w", err)
	}

	// Clone drops headers, the namespace among them, and reads
	// BAO_NAMESPACE again.
	api.SetNamespace(c.namespace)
	api.SetToken(token)
	secret, err := api.Logical().WriteWithContext(ctx, "sys/capabilities-self", map[string]any{"paths": []string{path}})
	// capabilities-self is in the default policy, so a refusal means the
	// token itself is no good.
	if responseErr, ok := errors.AsType[*openbao.ResponseError](err); ok && responseErr.StatusCode == http.StatusForbidden {
		return nil, ErrInvalidToken
	}

	if err != nil {
		return nil, fmt.Errorf("capabilities-self: %w", err)
	}

	if secret == nil {
		return nil, errors.New("capabilities-self: empty response")
	}

	raw, _ := secret.Data[path].([]any)
	capabilities := make([]string, 0, len(raw))
	for _, value := range raw {
		if capability, ok := value.(string); ok {
			capabilities = append(capabilities, capability)
		}
	}

	return capabilities, nil
}

// ReadField reads one non-empty string field at path with the proxy's
// token. A KV v2 response's inner data object is unwrapped.
func (c *Client) ReadField(ctx context.Context, path, field string) (string, error) {
	secret, err := c.api.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("read %q: no secret", path)
	}

	data := secret.Data
	if inner, ok := data["data"].(map[string]any); ok && data["metadata"] != nil {
		data = inner
	}

	value, ok := data[field].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("read %q: field %q is missing or empty", path, field)
	}

	return value, nil
}

// RenewSelf renews the proxy's own token and returns its new TTL. A
// periodic token renewed within its period never expires.
func (c *Client) RenewSelf(ctx context.Context) (time.Duration, error) {
	secret, err := c.api.Auth().Token().RenewSelfWithContext(ctx, 0)
	if err != nil {
		return 0, fmt.Errorf("renew token: %w", err)
	}

	if secret == nil || secret.Auth == nil {
		return 0, errors.New("renew token: empty response")
	}

	return time.Duration(secret.Auth.LeaseDuration) * time.Second, nil
}
