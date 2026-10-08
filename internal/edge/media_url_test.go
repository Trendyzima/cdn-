package edge

import (
  "net/http"
  "net/http/httptest"
  "testing"
  "strings"
  "github.com/Trendyzima/cdn-/internal/config"
)

func TestMediaURLContract(t *testing.T) {
  cfg := config.Config{CacheDir:t.TempDir(), MaxCacheBytes:1<<20, HotCacheBytes:1<<20, PublicBaseURL:"https://media.testagram.site", MediaURLPrefix:"/v1", RateLimitPerMin:100000, RateLimitBurst:100000}
  s := New(cfg)
  req := httptest.NewRequest(http.MethodGet, "/v1/media/url?path=users/abc/photo.webp", nil)
  rec := httptest.NewRecorder()
  s.Handler().ServeHTTP(rec, req)
  if rec.Code != http.StatusOK { t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String()) }
  body := rec.Body.String()
  if !strings.Contains(body, "https://media.testagram.site/v1/users/abc/photo.webp") { t.Fatalf("canonical CDN URL missing: %s", body) }
  if !strings.Contains(body, `"public":true`) { t.Fatalf("public media flag missing: %s", body) }
}