// Command openbao-git-proxy forwards git smart HTTP for allowed repositories
// to GitHub with one upstream credential, for Requesters whose OpenBao token
// holds a Grant on the repository's marker path.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xtruder/openbao-git-proxy/internal/config"
	"github.com/xtruder/openbao-git-proxy/internal/credential"
	"github.com/xtruder/openbao-git-proxy/internal/openbao"
	"github.com/xtruder/openbao-git-proxy/internal/proxy"
)

// tokenRenewInterval is far below the proxy token's recommended 720h
// period, so renewals may fail for weeks before the token expires.
const tokenRenewInterval = 72 * time.Hour

func main() {
	configPath := flag.String("config", "openbao-git-proxy.hcl", "path to the HCL configuration file")
	flag.Parse()
	if err := run(*configPath); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	bao, err := openbao.New(openbao.Config{
		Address: cfg.OpenBaoAddress, Namespace: cfg.OpenBaoNamespace, CAFile: cfg.OpenBaoCAFile, Token: cfg.OpenBaoToken,
	})
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	upstreamCredential, err := credential.Load(rootCtx, bao, cfg.CredentialPath, cfg.CredentialField)
	if err != nil {
		return fmt.Errorf("read the upstream credential: %w", err)
	}

	go tokenRenewer(rootCtx, logger, bao)
	go credentialRefresher(rootCtx, logger, upstreamCredential, cfg.RefreshInterval)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	handler := proxy.New(proxy.Options{
		Upstream:     cfg.Upstream,
		Transport:    transport,
		OpenBao:      bao,
		Credential:   upstreamCredential.Token,
		MarkerPrefix: cfg.MarkerPrefix,
		Repositories: cfg.Repositories,
		Logger:       logger,
	})

	server := newHTTPServer(cfg.ListenAddress, routes(handler))
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("listening", "address", cfg.ListenAddress, "repositories", cfg.Repositories)
		serveErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-rootCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
	}

	return nil
}

// newHTTPServer has no read or write timeout: a clone or push of a large
// repository streams for as long as it takes. Request contexts are cancelled
// when Shutdown begins, so transfers in flight don't hold it.
func newHTTPServer(address string, handler http.Handler) *http.Server {
	baseCtx, cancel := context.WithCancel(context.Background())
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	server.RegisterOnShutdown(cancel)
	return server
}

func routes(handler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/", handler)
	return mux
}

// tokenRenewer renews the proxy's own token at start and every
// tokenRenewInterval, so a periodic token never expires while the proxy
// runs. Failures are only logged: a token that isn't renewable still works
// until its own expiry.
func tokenRenewer(ctx context.Context, logger *slog.Logger, bao *openbao.Client) {
	renew := func() {
		ttl, err := bao.RenewSelf(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("renew proxy token; create it as a periodic token (period=720h renewable=true) "+
					"with update on auth/token/renew-self in its policy", "error", err)
			}

			return
		}

		logger.Info("renewed proxy token", "ttl", ttl.String())
	}

	renew()
	ticker := time.NewTicker(tokenRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			renew()
		case <-ctx.Done():
			return
		}
	}
}

// credentialRefresher re-reads the upstream credential every interval after
// the read at start, so a rotated PAT applies without a restart.
func credentialRefresher(ctx context.Context, logger *slog.Logger, upstream *credential.Credential, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := upstream.Refresh(ctx); err != nil && ctx.Err() == nil {
				logger.Error("refresh the upstream credential; keeping the last one", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
