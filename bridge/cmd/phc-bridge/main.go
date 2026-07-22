// Command phc-bridge serves the PHC project as a local, server-rendered website.
//
// Startup is resilient: it serves immediately from the on-disk cache (or an empty
// shell if none), then loads a fresh project from the STM in the background with
// bounded backoff. It never exits merely because the STM is unreachable, so it is
// safe under systemd Restart=on-failure and survives booting before the STM.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/cache"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/project"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/stm"
	bridgeweb "github.com/jtschneider/PHCRemoteControl/bridge/internal/web"
)

func main() {
	stmAddress := flag.String("stm", envOr("PHC_STM_ADDRESS", ""), "STM address, host[:port]")
	listenAddress := flag.String("listen", envOr("PHC_LISTEN_ADDRESS", "127.0.0.1:8080"), "website listen address")
	publicOrigin := flag.String("origin", envOr("PHC_PUBLIC_ORIGIN", "http://127.0.0.1:8080"), "exact public HTTP origin")
	stateDir := flag.String("state-dir", envOr("PHC_STATE_DIR", "/var/lib/phc-bridge"), "writable directory for the project cache")
	projectCache := flag.Bool("project-cache", envBool("PHC_PROJECT_CACHE", true), "persist the parsed project for instant, STM-independent startup")
	pollInterval := flag.Duration("poll-interval", envDuration("PHC_ACTIVE_POLL_INTERVAL", 2500*time.Millisecond), "active state polling interval")
	gracePeriod := flag.Duration("subscriber-grace", envDuration("PHC_SUBSCRIBER_GRACE_PERIOD", 15*time.Second), "polling grace period after the final browser disconnects")
	idleHealth := flag.Duration("idle-health-interval", envDuration("PHC_IDLE_HEALTH_INTERVAL", 0), "probe interval while no browser is connected (0 disables)")
	logLevel := flag.String("log-level", envOr("PHC_LOG_LEVEL", "info"), "log level: debug, info, warn, error")
	flag.Parse()

	logger := newLogger(*logLevel)

	if *stmAddress == "" {
		fatal(logger, errors.New("PHC_STM_ADDRESS or -stm is required"))
	}
	endpoint, err := stm.ParseEndpoint(*stmAddress)
	if err != nil {
		fatal(logger, err)
	}
	client := stm.NewClient(endpoint)
	controllerConfig := controller.Config{
		ActivePollInterval: *pollInterval,
		SubscriberGrace:    *gracePeriod,
		IdleHealthInterval: *idleHealth,
	}

	// Project cache — degrade to caching-disabled rather than refusing to start.
	var store *cache.Store
	if *projectCache {
		if err := os.MkdirAll(*stateDir, 0o700); err != nil {
			logger.Warn("cannot create state directory; running without a project cache",
				"state_dir", *stateDir, "error", err)
		} else {
			store = cache.New(*stateDir, *stmAddress)
		}
	}

	// Seed the first controller from the cache if we have one, otherwise an empty
	// shell. Either way the website comes up immediately.
	seed := domain.Project{}
	if cached, ok, err := store.Load(); err != nil {
		logger.Warn("ignoring unreadable project cache", "error", err)
	} else if ok {
		seed = cached
		logger.Info("loaded project from cache", "floors", len(seed.Floors))
	}
	control, err := controller.New(seed, client, controllerConfig)
	if err != nil {
		fatal(logger, err)
	}

	// reload downloads, parses, caches, and builds a fresh controller. Shared by
	// the reload endpoint and the background loader.
	reload := func(ctx context.Context, current bridgeweb.Backend) (bridgeweb.Backend, error) {
		runner, ok := current.(backgroundRunner)
		if !ok {
			return nil, errors.New("current controller cannot schedule project chunks")
		}
		zipData, err := project.Download(ctx, scheduledReader{client: client, runner: runner})
		if err != nil {
			return nil, err
		}
		files, err := project.Extract(zipData)
		if err != nil {
			return nil, err
		}
		parsed, err := project.Parse(files.PPFX, files.TPFX)
		if err != nil {
			return nil, err
		}
		if err := store.Save(parsed); err != nil {
			logger.Warn("failed to write project cache", "error", err)
		}
		return controller.New(parsed, client, controllerConfig)
	}

	website, err := bridgeweb.New(control, bridgeweb.Config{
		PublicOrigin: *publicOrigin, STMAddress: *stmAddress, Reload: reload, Logger: logger,
	})
	if err != nil {
		control.Close()
		fatal(logger, err)
	}

	// Background project loader: keep trying until a fresh project is installed.
	loaderCtx, cancelLoader := context.WithCancel(context.Background())
	var loaderWG sync.WaitGroup
	loaderWG.Add(1)
	go func() {
		defer loaderWG.Done()
		backoff := 2 * time.Second
		const maxBackoff = time.Minute
		for {
			if err := website.Reload(loaderCtx); err != nil {
				if loaderCtx.Err() != nil {
					return
				}
				logger.Warn("background project load failed; retrying", "error", err, "retry_in", backoff.String())
				select {
				case <-loaderCtx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff *= 2; backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			logger.Info("project loaded from STM")
			return
		}
	}()

	server := &http.Server{
		Addr: *listenAddress, Handler: website.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("PHC bridge website listening", "address", *listenAddress)
		serverErrors <- server.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
		logger.Info("shutting down PHC bridge")
	case err := <-serverErrors:
		cancelLoader()
		loaderWG.Wait()
		website.Close()
		if !errors.Is(err, http.ErrServerClosed) {
			fatal(logger, err)
		}
		return
	}

	cancelLoader()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
	}
	loaderWG.Wait()
	website.Close()
}

type backgroundRunner interface {
	RunBackground(context.Context, func(context.Context) (any, error)) (any, error)
}

type scheduledReader struct {
	client *stm.Client
	runner backgroundRunner
}

func (r scheduledReader) ReadFile(ctx context.Context, fileIndex, chunkIndex, mode int) (stm.FileChunk, error) {
	value, err := r.runner.RunBackground(ctx, func(callCtx context.Context) (any, error) {
		return r.client.ReadFile(callCtx, fileIndex, chunkIndex, mode)
	})
	if err != nil {
		return stm.FileChunk{}, err
	}
	chunk, ok := value.(stm.FileChunk)
	if !ok {
		return stm.FileChunk{}, fmt.Errorf("scheduled readFile returned %T", value)
	}
	return chunk, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s: %v\n", name, err)
		os.Exit(1)
	}
	return duration
}

func fatal(logger *slog.Logger, err error) {
	logger.Error("fatal", "error", err)
	os.Exit(1)
}
