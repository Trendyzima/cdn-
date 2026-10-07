package main

import (
    "log"
    "net/http"
    "os"
    "time"

    "github.com/Trendyzima/cdn-/internal/config"
    "github.com/Trendyzima/cdn-/internal/edge"
)

func main() {
    cfg := config.Load()
    server := edge.New(cfg)

    srv := &http.Server{
        Addr:              cfg.ListenAddr,
        Handler:           server.Handler(),
        ReadHeaderTimeout: 10 * time.Second,
        ReadTimeout:       30 * time.Second,
        WriteTimeout:      30 * time.Second,
        IdleTimeout:       120 * time.Second,
    }

    log.Printf("testagram edge listening on %s", cfg.ListenAddr)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Printf("server stopped: %v", err)
        os.Exit(1)
    }
}
