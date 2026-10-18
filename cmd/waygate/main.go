package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/logx"
	"github.com/zeroward/waygate/internal/mail"
	"github.com/zeroward/waygate/internal/soap"
	"github.com/zeroward/waygate/internal/status"
	"github.com/zeroward/waygate/internal/web"
)

func main() {
	syscall.Umask(0077)
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		flags := flag.NewFlagSet("migrate", flag.ExitOnError)
		path := flags.String("db", "data/kb.sqlite", "SQLite path; all other database clients must be stopped")
		flags.Parse(os.Args[2:])
		if err := migrateDatabase(*path); err != nil {
			slog.Error("migration failed; keep services stopped", "err", err)
			os.Exit(1)
		}
		slog.Info("database migration complete")
		return
	}

	if len(os.Args) > 1 {
		slog.Error("unsupported command; use waygate or waygate migrate")
		os.Exit(2)
	}
	cfg, err := config.Load()
	log := logx.New(envLogLevel(cfg))
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	if !cfg.DemoMode && cfg.CaptchaProvider == "none" {
		log.Warn("CAPTCHA_PROVIDER=none; registration is easier to abuse. Set turnstile or hcaptcha for production.")
	}
	if !cfg.DemoMode && strings.HasPrefix(strings.ToLower(cfg.SiteURL), "https://") && !cfg.SessionSecure {
		log.Warn("SITE_URL is https but SESSION_SECURE_COOKIE is false; the session cookie can leak on HTTP")
	}
	log.Info("hosts", "site", cfg.SiteURL, "realm", cfg.PublicHost)

	var database *db.DB
	if !cfg.DemoMode {
		database, err = db.Open(cfg)
		if err != nil {
			log.Error("mysql", "err", err)
			os.Exit(1)
		}
		defer database.Close()
		log.Info("mysql connected", "host", cfg.MySQLHost, "auth", cfg.AuthDB)
	} else {
		log.Info("demo mode: no MySQL, fake realm status")
	}

	var soapc *soap.Client
	if !cfg.DemoMode && cfg.SOAPConfigured() {
		soapc = soap.New(cfg.SOAPHost, cfg.SOAPPort, cfg.SOAPUsername, cfg.SOAPPassword, cfg.SOAPURI, cfg.SOAPTimeout)
		log.Info("SOAP client enabled", "host", cfg.SOAPHost, "port", cfg.SOAPPort)
	} else if !cfg.DemoMode && cfg.AccountMode != "sql" {
		log.Warn("SOAP not configured; account create will use SQL SRP6 fallback if ACCOUNT_CREATE_MODE allows it")
	}

	accounts := account.New(cfg, database, soapc)
	st := status.New(cfg, database, soapc)
	srv, err := web.New(cfg, log, accounts, st, captcha.New(cfg), mail.New(cfg))
	if err != nil {
		log.Error("http init", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// Body reads have no cap: Admin panel uploads and client zips can be multi-GB.
		ReadTimeout: 0,
		// No write timeout: the 3.3.5a client zip can be tens of gigabytes.
		WriteTimeout:   0,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "demo", cfg.DemoMode, "realm", cfg.RealmName)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shctx)
}

// migrateDatabase runs offline, without loading realm credentials or opening HTTP.
func migrateDatabase(path string) error {
	store, err := kb.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	_, err = identity.NewStore(store.SQL())
	return err
}

func envLogLevel(cfg config.Config) string {
	if cfg.LogLevel != "" {
		return cfg.LogLevel
	}
	return "info"
}
