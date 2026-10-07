package config

import (
  "testing"
  "time"
)

func TestLiveDefaults(t *testing.T) {
  t.Setenv("HOT_CACHE_BYTES", "")
  t.Setenv("MAX_CACHE_BYTES", "")
  t.Setenv("MAX_SEGMENT_BYTES", "")
  t.Setenv("TV_ORIGIN_TIMEOUT", "")
  c := Load()
  if c.HotCacheBytes != 32<<30 { t.Fatalf("hot cache default=%d, want %d", c.HotCacheBytes, 32<<30) }
  if c.MaxCacheBytes != 128<<30 { t.Fatalf("disk cache default=%d, want %d", c.MaxCacheBytes, 128<<30) }
  if c.MaxSegmentBytes != 32<<20 { t.Fatalf("segment limit=%d, want %d", c.MaxSegmentBytes, 32<<20) }
  if c.TVOriginTimeout != 30*time.Second { t.Fatalf("TV timeout=%s, want 30s", c.TVOriginTimeout) }
}
