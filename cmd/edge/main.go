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
    edgeServer := edge.New(cfg)

    publicServer := &http.Server{
        Addr:              cfg.ListenAddr,
        Handler:           edgeServer.Handler(),
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       15 * time.Second,
        WriteTimeout:      0, // live HLS/DASH delivery must not be killed by a fixed write deadline
        IdleTimeout:       180 * time.Second,
        MaxHeaderBytes:    32 << 10,
    }
    internalServer := &http.Server{
        Addr:              cfg.InternalListenAddr,
        Handler:           edgeServer.InternalHandler(),
        ReadHeaderTimeout: 5 * time.Second,
        ReadTimeout:       15 * time.Second,
        WriteTimeout:      15 * time.Second,
        IdleTimeout:       60 * time.Second,
        MaxHeaderBytes:    32 << 10,
    }

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        log.Printf("testagram edge public listener on %s node=%s", cfg.ListenAddr, cfg.NodeID)
        if err := publicServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Printf("public CDN server stopped: %v", err)
            os.Exit(1)
        }
    }()
    go func() {
        log.Printf("testagram edge private health/metrics listener on %s node=%s", cfg.InternalListenAddr, cfg.NodeID)
        if err := internalServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Printf("private CDN server stopped: %v", err)
            os.Exit(1)
        }
    }()

    <-stop
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    for _, server := range []*http.Server{publicServer, internalServer} {
        if err := server.Shutdown(ctx); err != nil {
            log.Printf("graceful CDN shutdown failed: %v", err)
            _ = server.Close()
        }
    }
}
