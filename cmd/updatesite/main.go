// Command updatesite serves a directory of release artifacts over HTTP.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mti/updatesite/internal/buildinfo"
	"github.com/mti/updatesite/internal/config"
	"github.com/mti/updatesite/internal/index"
	"github.com/mti/updatesite/internal/server"
)

func main() {
	// A scratch container has no shell or curl, so the binary doubles as its
	// own health probe: "updatesite -healthcheck".
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "-health") {
		os.Exit(healthcheck())
	}

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("updatesite: ")

	cfg := config.Load()

	idx := index.New(cfg.DataDir, cfg.CacheDir)
	srv, err := server.New(cfg, idx)
	if err != nil {
		log.Fatalf("cannot start: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go idx.Run(ctx, cfg.ScanInterval)
	go watchHup(ctx, idx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s (data %s, cache %s, rescan every %s)",
			cfg.Addr, cfg.DataDir, cfg.CacheDir, cfg.ScanInterval)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Printf("stopped (version %s, commit %s)", buildinfo.Version, buildinfo.Commit)
}

// healthcheck calls the local health endpoint. It is used by the container
// HEALTHCHECK, which cannot rely on a shell or curl in a scratch image.
func healthcheck() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	url := "http://" + addr + "/api/v1/health"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// watchHup triggers a rescan on SIGHUP so the archive can be refreshed without
// restarting the container. The signal never arrives on Windows, which is fine.
func watchHup(ctx context.Context, idx *index.Index) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			log.Printf("SIGHUP received, rescanning")
			idx.Rescan()
		}
	}
}
