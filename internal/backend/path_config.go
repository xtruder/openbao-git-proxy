package backend

import (
	"context"
	"net/url"
	"strings"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

const configKey = "config"

type config struct {
	ProxyURL string `json:"proxy_url"`
}

func (b *backend) pathConfig() *framework.Path {
	return &framework.Path{
		Pattern: "config",
		Fields: map[string]*framework.FieldSchema{
			"proxy_url": {
				Type:        framework.TypeString,
				Description: "Public HTTPS base URL of the proxy, used for the clone URLs that access paths return.",
			},
		},
		ExistenceCheck: func(ctx context.Context, request *logical.Request, _ *framework.FieldData) (bool, error) {
			entry, err := request.Storage.Get(ctx, configKey)
			return entry != nil, err
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation:   &framework.PathOperation{Callback: b.readConfig},
			logical.CreateOperation: &framework.PathOperation{Callback: b.writeConfig},
			logical.UpdateOperation: &framework.PathOperation{Callback: b.writeConfig},
		},
		HelpSynopsis: "The proxy's public URL.",
	}
}

func loadConfig(ctx context.Context, storage logical.Storage) (config, error) {
	var c config
	entry, err := storage.Get(ctx, configKey)
	if err != nil || entry == nil {
		return c, err
	}

	return c, entry.DecodeJSON(&c)
}

func (b *backend) readConfig(ctx context.Context, request *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	c, err := loadConfig(ctx, request.Storage)
	if err != nil {
		return nil, err
	}

	return &logical.Response{Data: map[string]any{"proxy_url": c.ProxyURL}}, nil
}

func (b *backend) writeConfig(ctx context.Context, request *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	c, err := loadConfig(ctx, request.Storage)
	if err != nil {
		return nil, err
	}

	// An update keeps proxy_url unless it names one.
	proxyURL := c.ProxyURL
	if value, ok := data.GetOk("proxy_url"); ok {
		proxyURL = strings.TrimSuffix(value.(string), "/")
	}

	if proxyURL != "" {
		parsed, parseErr := url.Parse(proxyURL)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return logical.ErrorResponse("proxy_url must be an https URL with a host and nothing else"), nil //nolint:nilerr // A bad URL is the caller's error, reported as such.
		}
	}

	entry, err := logical.StorageEntryJSON(configKey, config{ProxyURL: proxyURL})
	if err != nil {
		return nil, err
	}

	return nil, request.Storage.Put(ctx, entry)
}
