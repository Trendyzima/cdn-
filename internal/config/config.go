package config

import (
    "os"
    "strconv"
    "strings"
    "time"
)

type Config struct {
    ListenAddr        string
    OriginURL         string
    CacheDir          string
    CacheTTL          time.Duration
    MaxCacheBytes     int64
    SegmentTTL        time.Duration
    ManifestTTL       time.Duration
    MaxSegmentBytes   int64
    AllowedOrigins    []string
    NodeID            string
}

func Load() Config {
    return Config{
        ListenAddr:      env("LISTEN_ADDR", ":8080"),
        OriginURL:       strings.TrimRight(os.Getenv("ORIGIN_URL"), "/"),
        CacheDir:        env("CACHE_DIR", "./cache"),
        CacheTTL:        duration("CACHE_TTL", 30*time.Second),
        MaxCacheBytes:   integer("MAX_CACHE_BYTES", 2<<30),
        SegmentTTL:      duration("SEGMENT_TTL", 20*time.Second),
        ManifestTTL:     duration("MANIFEST_TTL", 2*time.Second),
        MaxSegmentBytes: integer("MAX_SEGMENT_BYTES", 16<<20),
        AllowedOrigins:  split(os.Getenv("ALLOWED_ORIGINS")),
        NodeID:          env("NODE_ID", "local"),
    }
}

func env(k, fallback string) string {
    if v := os.Getenv(k); v != "" { return v }
    return fallback
}
func duration(k string, fallback time.Duration) time.Duration {
    if v := os.Getenv(k); v != "" {
        if d, err := time.ParseDuration(v); err == nil { return d }
    }
    return fallback
}
func integer(k string, fallback int64) int64 {
    if v := os.Getenv(k); v != "" {
        if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 { return n }
    }
    return fallback
}
func split(v string) []string {
    if v == "" { return nil }
    p := strings.Split(v, ",")
    out := make([]string, 0, len(p))
    for _, x := range p {
        if x = strings.TrimSpace(x); x != "" { out = append(out, x) }
    }
    return out
}
