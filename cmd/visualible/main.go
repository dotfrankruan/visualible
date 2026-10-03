// Command visualible starts the Visualible server: a visual IDE and
// deployment control plane for Ansible, distributed as a single binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/server"
	"github.com/dotfrankruan/visualible/internal/store"
)

var version = "0.1.0"

// secretResolver adapts the credential store to the deploy.SecretResolver
// contract, keeping secret material out of the HTTP layer entirely.
type secretResolver struct {
	st *store.Store
}

func (s secretResolver) ResolveSecret(ctx context.Context, credentialID string) (string, []byte, error) {
	kind, err := s.st.CredentialKind(ctx, credentialID)
	if err != nil {
		return "", nil, err
	}
	secret, err := s.st.CredentialSecret(ctx, credentialID)
	if err != nil {
		return "", nil, err
	}
	return kind, secret, nil
}

func main() {
	var (
		addr    = flag.String("addr", envOr("VISUALIBLE_ADDR", "127.0.0.1"), "bind address")
		port    = flag.Int("port", envInt("VISUALIBLE_PORT", 8080), "bind port")
		dataDir = flag.String("data-dir", envOr("VISUALIBLE_DATA_DIR", defaultDataDir()), "application data directory")
	)
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		logger.Fatalf("data dir: %v", err)
	}

	discovery, err := ansible.NewDiscovery(ctx, filepath.Join(*dataDir, "cache"))
	if err != nil {
		logger.Fatalf("ansible discovery: %v", err)
	}

	dbPath := filepath.Join(*dataDir, "visualible.db")
	st, err := store.Open(dbPath, store.SecretKeyPath(*dataDir))
	if err != nil {
		logger.Fatalf("open database: %v", err)
	}
	defer st.Close()

	// Apply persisted Ansible path overrides (if any) before serving.
	if settings, err := st.GetSettings(ctx); err == nil {
		discovery.ApplyOverrides(ctx, settings.AnsiblePath, settings.AnsibleDocPath, settings.AnsiblePlaybookPath)
	}

	backend := deploy.NewAnsibleBackend(discovery.Installation())
	manager := deploy.NewManager(backend, st, secretResolver{st})

	server.Version = version
	srv, err := server.New(discovery, st, manager)
	if err != nil {
		logger.Fatalf("server: %v", err)
	}

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Startup banner: the explicit, inspectable boot summary.
	fmt.Printf("Visualible v%s\n", version)
	if st := discovery.Status(); st.Available {
		fmt.Printf("Ansible: %s detected (%s)\n", st.Version, st.Path)
	} else {
		fmt.Println("Ansible: not detected (install Ansible to enable module discovery and deployment)")
	}
	if mods, ok := discovery.CachedModules(); ok {
		fmt.Printf("Modules: %d discovered\n", len(mods))
	} else {
		fmt.Println("Modules: not yet discovered (will populate on first use)")
	}
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Printf("Listening: http://%s\n", listen)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		logger.Fatalf("http: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "visualible")
	}
	return filepath.Join(".", "visualible-data")
}
