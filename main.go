// Command mirror-studio serves the whole application from a single process:
// embedded migrations, an idempotent seed, in-process background workers for
// generation and novel visualisation, and the HTTP listener.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // the tz database is embedded so a scratch image still resolves TIME_ZONE

	"mirror-studio/internal/audit"
	"mirror-studio/internal/auth"
	"mirror-studio/internal/bootstrap"
	"mirror-studio/internal/config"
	"mirror-studio/internal/console"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/generator"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/media"
	"mirror-studio/internal/novel"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/risk"
	"mirror-studio/internal/siteconfig"
	"mirror-studio/internal/web"
)

//go:embed all:static
var staticAssets embed.FS

// version is injected at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "", "listen address (overrides APP_LISTEN)")
	flag.Parse()

	cfg, err := config.Load(version)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.ListenAddr = *listen
	}

	logger := newLogger(cfg)
	logger.Info("starting mirror-studio", "version", version, "debug", cfg.Debug)

	location, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return fmt.Errorf("TIME_ZONE %q is not a known time zone: %w", cfg.TimeZone, err)
	}

	handle, err := db.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer handle.Close()

	if err := handle.Migrate(logger); err != nil {
		return err
	}

	cipher, err := crypt.NewFieldCipher(cfg.FieldCryptKey)
	if err != nil {
		return err
	}

	site := siteconfig.New(handle, cipher, logger)
	site.Env = os.Getenv

	auditService := audit.NewService(handle, logger)
	quotaService := quota.NewService(handle, cfg, site, logger)
	riskService := risk.NewService(handle, site, cfg, logger)

	mediaService := media.NewService(media.Deps{
		DB: handle, Config: cfg, Site: site, Logger: logger,
	})
	mediaHandlers := mediaService.NewHandlers()

	generatorService := generator.NewService(generator.Deps{
		DB: handle, Config: cfg, Site: site, Location: location,
		Risk: riskService, Quota: quotaService, Media: mediaService,
		Audit: auditService, Cipher: cipher, Logger: logger,
	})
	generatorHandlers := generatorService.NewHandlers()

	novelService := novel.NewService(novel.Deps{
		DB: handle, Config: cfg, Site: site,
		Generator: novelChannelResolver{generator: generatorService, cipher: cipher, logger: logger},
		Media:     mediaService,
		Log:       logger,
	})
	novelHandlers := novelService.NewHandlers()
	generatorService.SetNovelHistory(novelService)

	authService := auth.NewService(handle, cfg, cipher, site, location, logger)
	authService.Quota = quotaService
	authService.Risk = riskService
	authService.Audit = auditService
	authService.Tasks = generatorService
	authHandlers := authService.NewHandlers(site, cfg, logger)

	consoleService := console.NewService(console.Deps{
		DB: handle, Config: cfg, Site: site, Location: location,
		Auth: authService, Quota: quotaService, Generator: generatorService,
		Media: mediaService, Logger: logger,
	})
	consoleHandlers := consoleService.NewHandlers()

	webService := web.NewService(web.Deps{
		DB: handle, Config: cfg, Site: site, Location: location, Cipher: cipher,
		Auth:   webAuthAdapter{service: authService},
		Risk:   riskService,
		Quota:  quotaService,
		Media:  mediaService,
		Logger: logger,
	})

	// ── seed ──
	if err := bootstrap.Run(context.Background(), bootstrap.Options{
		Config: cfg, Site: site, Admin: authService, Channel: generatorService, Logger: logger,
	}); err != nil {
		return err
	}

	// ── routing ──
	mux := http.NewServeMux()
	authHandlers.Register(mux)
	mediaHandlers.Register(mux)
	generatorHandlers.Register(mux)
	novelHandlers.Register(mux)
	consoleHandlers.Register(mux)
	webService.Register(mux)

	assets, err := fs.Sub(staticAssets, ".")
	if err != nil {
		return fmt.Errorf("open embedded static assets: %w", err)
	}

	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   cfg,
		Logger:   logger,
		Sessions: httpapi.NewSessionStore(handle, !cfg.Debug),
		ResolveUser: func(ctx context.Context, userID int64) (httpapi.Principal, error) {
			return authService.ResolvePrincipal(ctx, userID)
		},
		IPBlocked:      riskService.IPBlocked,
		TrustedOrigins: cfg.CSRFTrustedOrigins,
	}, httpapi.NewRouter(mux))
	middleware.Static = httpapi.NewStaticHandler(assets, "static")
	middleware.RateLimiter = riskService.Counters().Increment

	// ── background work ──
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	generatorService.StartWorker(ctx)
	if requeued, err := novelService.RequeueInterruptedTasks(ctx); err != nil {
		logger.Warn("小说可视化任务重启恢复失败", "error", err)
	} else if requeued > 0 {
		logger.Info("小说可视化任务重启后重新排队", "count", requeued)
	}
	novelService.StartWorker(ctx)
	go runMaintenanceLoop(ctx, logger, handle, site, mediaService)

	// ── listener ──
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           middleware,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	logger.Info("listening", "addr", listener.Addr().String())

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case received := <-signals:
		logger.Info("shutting down", "signal", received.String())
	}

	// Stop accepting, then let the workers finish the tasks they already claimed.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown did not complete cleanly", "error", err)
	}
	cancel()
	generatorService.StopWorker()
	novelService.StopWorker()
	if err := handle.Close(); err != nil {
		logger.Warn("database close failed", "error", err)
	}
	logger.Info("stopped")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

// novelChannelResolver adapts generator.ResolveChannel to the shape
// internal/novel declares. novel cannot import generator's Channel type for the
// same reason generator cannot import novel.Service: internal/novel depends on
// internal/generator for the LLM-channel lookup, so the two packages meet
// through narrow local interfaces and this adapter joins them.
type novelChannelResolver struct {
	generator *generator.Service
	cipher    *crypt.FieldCipher
	logger    *slog.Logger
}

func (resolver novelChannelResolver) ResolveChannel(ctx context.Context, code string) (*novel.Channel, error) {
	channel, err := resolver.generator.ResolveChannel(ctx, code)
	if err != nil || channel == nil {
		return nil, err
	}
	apiKey, err := channel.APIKey(resolver.cipher)
	if err != nil {
		// A channel whose stored key cannot be decrypted is treated as having no
		// key: the novel side then falls back to the environment configuration,
		// exactly as it does for a channel lookup that returns nothing.
		resolver.logger.Warn("LLM 渠道密钥解密失败", "channel_code", channel.ChannelCode, "error", err)
		apiKey = ""
	}
	return &novel.Channel{
		BaseURL:   channel.BaseURL,
		APIPath:   channel.APIPath,
		ModelName: channel.ModelName,
		Name:      channel.Name,
		APIKey:    apiKey,
	}, nil
}

// webAuthAdapter narrows *auth.Service to the httpapi.Principal-based interface
// internal/web declares. The SSR pages only ever need a principal, so the
// adapter drops the concrete *auth.User the domain returns.
type webAuthAdapter struct {
	service *auth.Service
}

func (adapter webAuthAdapter) ByID(ctx context.Context, id int64) (httpapi.Principal, error) {
	return adapter.service.ResolvePrincipal(ctx, id)
}

func (adapter webAuthAdapter) ByUsername(ctx context.Context, username string) (httpapi.Principal, error) {
	user, err := adapter.service.ByUsername(ctx, username)
	if err != nil || user == nil {
		return nil, err
	}
	return adapter.service.ResolvePrincipal(ctx, user.ID())
}

func (adapter webAuthAdapter) Authenticate(ctx context.Context, username, password string) (httpapi.Principal, error) {
	user, err := adapter.service.Authenticate(ctx, username, password)
	if err != nil || user == nil {
		return nil, err
	}
	return adapter.service.ResolvePrincipal(ctx, user.ID())
}

func (adapter webAuthAdapter) Register(ctx context.Context, username, email, password, ip string) (httpapi.Principal, error) {
	user, err := adapter.service.Register(ctx, username, email, password, ip)
	if err != nil || user == nil {
		return nil, err
	}
	return adapter.service.ResolvePrincipal(ctx, user.ID())
}

func (adapter webAuthAdapter) SetPassword(ctx context.Context, userID int64, password string) error {
	return adapter.service.SetPassword(ctx, userID, password)
}

func (adapter webAuthAdapter) ValidateRegisterEmailCode(email, code string) error {
	return adapter.service.ValidateRegisterEmailCode(email, code)
}

// runMaintenanceLoop performs the periodic housekeeping the reference ran from
// management commands: expired sessions, expired upload sessions and media
// retention. It never retries silently: every failure is logged with backoff.
func runMaintenanceLoop(
	ctx context.Context,
	logger *slog.Logger,
	handle *db.DB,
	site *siteconfig.Service,
	mediaService *media.Service,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Error("maintenance loop crashed", "panic", recovered)
		}
	}()
	interval := 5 * time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("maintenance pass crashed", "panic", recovered)
				}
			}()
			if _, err := (&httpapi.SessionStore{DB: handle}).PurgeExpired(); err != nil {
				logger.Warn("session purge failed", "error", err)
			}
			counts, err := mediaService.CleanupStorage(ctx, 200)
			if err != nil {
				logger.Warn("media cleanup failed", "error", err)
				return
			}
			if len(counts) > 0 {
				logger.Info("media cleanup finished", "counts", counts)
			}
		}()
	}
}
