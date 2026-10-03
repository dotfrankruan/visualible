// Command visualible starts the Visualible server: a visual IDE and
// deployment control plane for Ansible, distributed as a single binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dotfrankruan/visualible/internal/ansible"
	"github.com/dotfrankruan/visualible/internal/deploy"
	"github.com/dotfrankruan/visualible/internal/logging"
	"github.com/dotfrankruan/visualible/internal/server"
	"github.com/dotfrankruan/visualible/internal/store"
)

var version = "0.1.0"

func main() {
	var (
		addr      = flag.String("addr", envOr("VISUALIBLE_ADDR", "127.0.0.1"), "bind address")
		port      = flag.Int("port", envInt("VISUALIBLE_PORT", 8080), "bind port")
		dataDir   = flag.String("data-dir", envOr("VISUALIBLE_DATA_DIR", defaultDataDir()), "application data directory")
		verbose   = flag.Bool("verbose", envBool("VISUALIBLE_VERBOSE", false), "log everything, including DEBUG detail")
		logLevel  = flag.String("log-level", envOr("VISUALIBLE_LOG_LEVEL", ""), "log level: debug, info, warn, error, fatal")
		logFormat = flag.String("log-format", envOr("VISUALIBLE_LOG_FORMAT", "text"), "log format: text or json")
	)
	flag.Parse()

	// Verbose is shorthand for debug; an explicit -log-level wins.
	level := *logLevel
	if level == "" && *verbose {
		level = "debug"
	}
	if err := logging.Setup(logging.Config{Level: level, Format: *logFormat}); err != nil {
		fmt.Fprintf(os.Stderr, "invalid logging configuration: %v\n", err)
		os.Exit(2)
	}

	logging.Info("visualible starting",
		"version", version,
		"logLevel", logging.LevelName(logging.Current()),
		"logFormat", *logFormat,
		"dataDir", *dataDir,
		"pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		logging.Fatal("data directory is not usable", "path", *dataDir, "err", err)
	}

	discovery, err := ansible.NewDiscovery(ctx, filepath.Join(*dataDir, "cache"))
	if err != nil {
		logging.Fatal("ansible discovery failed", "err", err)
	}
	if st := discovery.Status(); st.Available {
		mods := 0
		if cached, ok := discovery.CachedModules(); ok {
			mods = len(cached)
		}
		logging.Info("ansible detected", "version", st.Version, "path", st.Path, "cachedModules", mods)
	} else {
		logging.Warn("ansible was not detected; module discovery and deployment are unavailable")
	}

	dbPath := filepath.Join(*dataDir, "visualible.db")
	st, err := store.Open(dbPath, store.SecretKeyPath(*dataDir))
	if err != nil {
		logging.Fatal("could not open the database", "path", dbPath, "err", err)
	}
	defer st.Close()
	logging.Debug("database opened", "path", dbPath, "keyFile", store.SecretKeyPath(*dataDir))

	// Apply persisted Ansible path overrides (if any) before serving.
	if settings, err := st.GetSettings(ctx); err == nil {
		if settings.AnsiblePath != "" || settings.AnsibleDocPath != "" || settings.AnsiblePlaybookPath != "" {
			logging.Info("applying configured ansible paths",
				"ansible", settings.AnsiblePath,
				"ansibleDoc", settings.AnsibleDocPath,
				"ansiblePlaybook", settings.AnsiblePlaybookPath)
		}
		discovery.ApplyOverrides(ctx, settings.AnsiblePath, settings.AnsibleDocPath, settings.AnsiblePlaybookPath)
	} else {
		logging.Warn("could not read settings", "err", err)
	}

	backend := deploy.NewAnsibleBackend(discovery.Installation())
	manager := deploy.NewManager(backend, st, st)
	logging.Debug("deployment backend ready",
		"backend", backend.ID(),
		"name", backend.Metadata().Name)

	server.Version = version
	srv, err := server.New(discovery, st, manager)
	if err != nil {
		logging.Fatal("could not build the server", "err", err)
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
	fmt.Printf("Logging: %s (%s)\n", logging.LevelName(logging.Current()), *logFormat)
	if logging.Enabled(logging.LevelDebug) {
		fmt.Println("Verbose: on — DEBUG detail is printed to stderr")
	}
	fmt.Printf("Listening: http://%s\n", listen)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	logging.Info("listening", "url", "http://"+listen)

	select {
	case <-ctx.Done():
		logging.Info("shutdown signal received; stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		logging.Info("stopped")
	case err := <-errCh:
		logging.Fatal("http server failed", "err", err)
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

func envBool(key string, fallback bool) bool {
	switch v := os.Getenv(key); v {
	case "1", "true", "TRUE", "yes", "on":
		return true
	case "0", "false", "FALSE", "no", "off":
		return false
	}
	return fallback
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "visualible")
	}
	return filepath.Join(".", "visualible-data")
}
