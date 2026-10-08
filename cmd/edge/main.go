package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/Trendyzima/cdn-/internal/config"
    "github.com/Trendyzima/cdn-/internal/edge"
)

func main() {
    cfg := config.Load()
    handler := edge.New(cfg).Handler()

    srv := &http.Server{
        Addr:              cfg.ListenAddr,
        Handler:           handler,
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       15 * time.Second,
        WriteTimeout:      0, // live HLS/DASH delivery must not be killed by a fixed write deadline
        IdleTimeout:       180 * time.Second,
        MaxHeaderBytes:    32 << 10,
    }

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        log.Printf("testagram edge listening on %s node=%s", cfg.ListenAddr, cfg.NodeID)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Printf("server stopped: %v", err)
            os.Exit(1)
        }
    }()

    <-stop
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if err := srv.Shutdown(ctx); err != nil {
        log.Printf("graceful shutdown failed: %v", err)
        _ = srv.Close()
    }
}
