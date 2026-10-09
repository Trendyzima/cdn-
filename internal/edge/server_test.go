package edge

import (
 "encoding/json"
 "net/url"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
 "strings"
 "github.com/Trendyzima/cdn-/internal/config"
)

func TestCleanAssetPath(t *testing.T){
 tests:=[]struct{in string;ok bool;want string}{
  {"/v1/live/channel/index.m3u8",true,"live/channel/index.m3u8"},
  {"/v1/live/../secret",false,""},
  {"/v1/../../secret",false,""},
  {"/v1/",false,""},
  {"/v1/users/u1/%2e%2e/secret.webp",false,""},
  {"/v1/users/u1/../secret.webp",false,""},
  {"/v1/etc/passwd",false,""},
  {"/v1/users/u1/image.webp",true,"users/u1/image.webp"},
 }
 for _,tt:=range tests{got,ok:=cleanAssetPath(tt.in);if ok!=tt.ok||got!=tt.want{t.Fatalf("%q => %q,%v",tt.in,got,ok)}}
}
func TestPlaybackToken(t *testing.T){
 s:=&Server{cfg:config.Config{PlaybackSecret:"secret"}}
 token:="9999999999."+signToken("live/a.ts","secret","9999999999")
 if !s.authorized("live/a.ts",token){t.Fatal("valid token rejected")}
 if s.authorized("live/b.ts",token){t.Fatal("token accepted for another path")}
}
func TestHealth(t *testing.T){
 s:=New(testConfig(t))
 rr:=httptest.NewRecorder()
 s.health(rr,httptest.NewRequest("GET","/healthz",nil))
 if rr.Code!=200{t.Fatalf("health status %d",rr.Code)}
}
func testConfig(t *testing.T)config.Config{
 return config.Config{
  CacheDir:t.TempDir(),MaxCacheBytes:1<<20,SegmentTTL:time.Minute,ManifestTTL:time.Second,
  MaxSegmentBytes:1<<20,NodeID:"test",OriginURLs:[]string{"http://127.0.0.1:1"},
  PlaybackSecret:"secret",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second,
 }
}
func TestMediaCachePolicy(t *testing.T) {
 if got:=cacheControl("users/a/thumbnail.webp"); !strings.Contains(got, "immutable") { t.Fatalf("image cache policy not immutable: %s", got) }
 if got:=cacheControl("live/channel/seg-1.m4s"); !strings.Contains(got, "s-maxage=120") { t.Fatalf("segment cache policy changed unexpectedly: %s", got) }
 if got:=cacheControl("live/channel/index.m3u8"); !strings.Contains(got, "max-age=0") { t.Fatalf("manifest cache policy changed unexpectedly: %s", got) }
}

func TestLegacyMediaAliasesReachAssetHandler(t *testing.T) {
 s := New(testConfig(t))
 for _, route := range []string{
  "/users/abc/image.webp",
  "/profiles/abc/avatar.webp",
  "/media/users/abc/image.webp",
 } {
  rr := httptest.NewRecorder()
  s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", route, nil))
  if rr.Code == 404 || rr.Code == 400 { t.Fatalf("legacy media route %s was rejected before reaching origin: status=%d", route, rr.Code) }
 }
}


func TestXCloneMediaAliases(t *testing.T) {
 s := New(testConfig(t))
 for _, route := range []string{
  "/uploads/posts/123/image.jpg",
  "/avatars/users/123/avatar.webp",
  "/covers/users/123/header.png",
  "/photos/posts/123/photo.jpg",
  "/videos/posts/123/video.mp4",
 } {
  rr := httptest.NewRecorder()
  s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", route, nil))
  if rr.Code == 400 || rr.Code == 404 { t.Fatalf("XClone media alias %s rejected: %d", route, rr.Code) }
 }
}

func TestMediaURLContract(t *testing.T) {
 cfg := testConfig(t)
 cfg.PublicBaseURL = "https://media.testagram.site"
 cfg.MediaURLPrefix = "/v1"
 s := New(cfg)
 rr := httptest.NewRecorder()
 s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/media/url?path=users/u1/avatar.webp", nil))
 if rr.Code != 200 { t.Fatalf("media URL status=%d", rr.Code) }
 if !strings.Contains(rr.Body.String(), "https://media.testagram.site/v1/users/u1/avatar.webp") {
  t.Fatalf("unexpected media URL: %s", rr.Body.String())
 }
}

func TestR2MediaOrigin(t *testing.T) {
 origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.URL.Path != "/users/u1/avatar.webp" { t.Fatalf("unexpected R2 path: %s", r.URL.Path) }
  w.Header().Set("Content-Type", "image/webp")
  _, _ = w.Write(make([]byte, 2048))
 }))
 defer origin.Close()
 cfg := testConfig(t)
 cfg.R2PublicBaseURL = origin.URL
 s := New(cfg)
 data, ok := s.fetchR2("users/u1/avatar.webp")
 if !ok || len(data) != 2048 { t.Fatalf("R2 media origin failed: ok=%v bytes=%d", ok, len(data)) }
}

func TestPrivateMediaURLProducesAuthorizedToken(t *testing.T) {
 cfg := testConfig(t)
 cfg.PublicBaseURL = "https://media.testagram.site"
 cfg.MediaURLPrefix = "/v1"
 cfg.PlaybackSecret = "test-secret"
 s := New(cfg)
 rr := httptest.NewRecorder()
 s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/media/url?path=users/u1/private.webp&private=1", nil))
 if rr.Code != http.StatusOK { t.Fatalf("media URL status=%d", rr.Code) }
 var body struct { URL string `json:"url"`; ExpiresAt int64 `json:"expires_at"`; Public bool `json:"public"` }
 if err := json.NewDecoder(rr.Body).Decode(&body); err != nil { t.Fatal(err) }
 if body.Public || body.ExpiresAt <= time.Now().Unix() { t.Fatalf("unexpected private media metadata: %+v", body) }
 u, err := url.Parse(body.URL); if err != nil { t.Fatal(err) }
 if !s.authorized("users/u1/private.webp", u.Query().Get("token")) { t.Fatalf("generated private token was not accepted") }
}


func TestValidateRangeHeader(t *testing.T) {
 tests := []struct{ header string; ok bool }{
  {"", true},
  {"bytes=0-1023", true},
  {"bytes=0-", true},
  {"bytes=-1024", true},
  {"bytes=0-1048575", true},
  {"bytes=", false},
  {"bytes=-0", false},
  {"bytes=1048576-", false},
  {"bytes=0-1048576", false},
  {"bytes=0-1,4-5", false},
  {"bytes=5-4", false},
  {"items=0-1", false},
 }
 for _, tt := range tests {
  ok, _ := validateRangeHeader(tt.header, 1<<20)
  if ok != tt.ok { t.Fatalf("%q => %v, want %v", tt.header, ok, tt.ok) }
 }
}

func TestPrivateMediaResponseIsNeverPubliclyCacheable(t *testing.T) {
 origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "image/webp")
  _, _ = w.Write([]byte("private"))
 }))
 defer origin.Close()
 cfg := testConfig(t)
 cfg.OriginURLs = []string{origin.URL}
 s := New(cfg)
 rr := httptest.NewRecorder()
 req := httptest.NewRequest("GET", "/v1/users/u1/private.webp?token=9999999999."+signToken("users/u1/private.webp","secret","9999999999"), nil)
 s.Handler().ServeHTTP(rr, req)
 if rr.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String()) }
 if got := rr.Header().Get("Cache-Control"); got != "private, no-store" { t.Fatalf("private cache-control=%q", got) }
 if got := rr.Header().Get("Vary"); got != "Authorization, Range" { t.Fatalf("private vary=%q", got) }
}


func TestIPTVCORSContract(t *testing.T) {
 cfg := testConfig(t)
 cfg.AllowedOrigins = []string{"https://testagram.site"}
 s := New(cfg)

 rr := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodOptions, "/v1/tv/live/seg.ts", nil)
 req.Header.Set("Origin", "https://testagram.site")
 req.Header.Set("Access-Control-Request-Headers", "range")
 s.Handler().ServeHTTP(rr, req)
 if rr.Code != http.StatusNoContent { t.Fatalf("preflight status=%d", rr.Code) }
 if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://testagram.site" { t.Fatalf("allow-origin=%q", got) }
 if got := rr.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Range") || !strings.Contains(got, "Authorization") { t.Fatalf("allow-headers=%q", got) }
 if got := rr.Header().Get("Access-Control-Max-Age"); got != "600" { t.Fatalf("max-age=%q", got) }

 blocked := httptest.NewRecorder()
 blockedReq := httptest.NewRequest(http.MethodOptions, "/v1/tv/live/seg.ts", nil)
 blockedReq.Header.Set("Origin", "https://evil.example")
 s.Handler().ServeHTTP(blocked, blockedReq)
 if blocked.Code != http.StatusForbidden { t.Fatalf("blocked preflight status=%d", blocked.Code) }
}

func TestTVPrefetchMetricsAreExposed(t *testing.T) {
 s := New(testConfig(t))
 s.tvPrefetchHits.Store(2)
 s.tvPrefetchMisses.Store(3)
 s.tvPrefetchSuccesses.Store(2)
 s.tvPrefetchFailures.Store(1)
 s.tvPrefetchWarmedSeconds.Store(30)

 health := httptest.NewRecorder()
 s.health(health, httptest.NewRequest("GET", "/healthz", nil))
 var payload map[string]any
 if err := json.Unmarshal(health.Body.Bytes(), &payload); err != nil { t.Fatalf("decode health payload: %v", err) }
 for _, key := range []string{"tv_prefetch_hits", "tv_prefetch_misses", "tv_prefetch_successes", "tv_prefetch_failures", "tv_prefetch_warmed_seconds"} {
  if _, ok := payload[key]; !ok { t.Errorf("health response missing %q", key) }
 }

 metrics := httptest.NewRecorder()
 s.metrics(metrics, httptest.NewRequest("GET", "/metrics", nil))
 body := metrics.Body.String()
 for _, metric := range []string{
  "testagram_edge_tv_prefetch_cache_hits_total 2",
  "testagram_edge_tv_prefetch_cache_misses_total 3",
  "testagram_edge_tv_prefetch_successes_total 2",
  "testagram_edge_tv_prefetch_failures_total 1",
  "testagram_edge_tv_prefetch_warmed_seconds_total 30",
 } {
  if !strings.Contains(body, metric) { t.Errorf("metrics response missing %q", metric) }
 }
}
