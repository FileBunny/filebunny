// Command filebunny runs the FileBunny backend.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/cleanup"
	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/server"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Safe only here, before the server accepts uploads.
	if n, err := st.RemoveOrphans(context.Background()); err != nil {
		return err
	} else if n > 0 {
		log.Info("removed orphaned files", "count", n)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Stop cleanup and wait for it on every return path, before st.Close.
	var wg sync.WaitGroup
	defer func() { stop(); wg.Wait() }()
	wg.Go(func() { cleanup.Run(ctx, st, cfg.CleanupInterval, log) })

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(cfg, st, log),
		ErrorLog:          server.ErrorLog(log),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
