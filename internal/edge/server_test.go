package edge

import (
 "fmt"
 "encoding/json"
 "context"
 "sync/atomic"
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
  {"/v1/public/site/logo.webp",true,"public/site/logo.webp"},
 }
 for _,tt:=range tests{got,ok:=cleanAssetPath(tt.in);if ok!=tt.ok||got!=tt.want{t.Fatalf("%q => %q,%v",tt.in,got,ok)}}
}
func TestPlaybackTokenQueryIsRejected(t *testing.T) {
 cfg:=testConfig(t);s:=New(cfg);rr:=httptest.NewRecorder()
 s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/users/u1/avatar.webp?token=legacy",nil))
 if rr.Code!=http.StatusBadRequest {t.Fatalf("legacy token status=%d body=%s",rr.Code,rr.Body.String())}
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
  OriginAuthToken:"test-origin-token",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second,TVPrefetchTimeout:12*time.Second,
 }
}
func TestMediaCachePolicy(t *testing.T) {
 if got:=cacheControl("users/a/thumbnail.webp"); !strings.Contains(got, "max-age=0") || !strings.Contains(got,"s-maxage=300") || strings.Contains(got,"immutable") { t.Fatalf("mutable image cache policy is unsafe: %s", got) }
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

func TestMediaURLIssuanceIsDisabledForCallerControlledPaths(t *testing.T) {
	cfg := testConfig(t)
	cfg.PublicBaseURL = "https://media.testagram.site"
	s := New(cfg)
	for _, target := range []string{
		"/v1/media/url?path=users/u1/avatar.webp",
		"/v1/media/url?path=users/u1/private.webp&private=1",
		"/v1/media/url?path=users/victim/private.webp&private=1",
	} {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != http.StatusGone {
			t.Fatalf("untrusted URL helper %s returned status=%d body=%s", target, rr.Code, rr.Body.String())
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("disabled URL helper cache policy=%q", rr.Header().Get("Cache-Control"))
		}
		if strings.Contains(rr.Body.String(), "media.testagram.site") || strings.Contains(rr.Body.String(), "token=") {
			t.Fatalf("disabled URL helper leaked a media URL: %s", rr.Body.String())
		}
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


func TestPrivateMediaRequestWithoutViewerIdentityIsRejectedByOrigin(t *testing.T) {
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"viewer identity required",http.StatusUnauthorized)}))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};s:=New(cfg);rr:=httptest.NewRecorder()
 s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/users/u1/private.webp",nil))
 if rr.Code!=http.StatusUnauthorized {t.Fatalf("private unauthenticated status=%d",rr.Code)}
 if strings.Contains(rr.Header().Get("Cache-Control"),"public"){t.Fatalf("private response publicly cacheable: %q",rr.Header().Get("Cache-Control"))}
}

func TestBackendAssertionBypassesCacheAndForwardsIdentity(t *testing.T) {
 seen:=false
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/internal/cdn/v1/users/u1/private.webp"{t.Errorf("unexpected trusted origin path: %s",r.URL.Path)}
  if r.Header.Get("X-Testagram-Origin-Auth")!="origin-secret"{t.Errorf("missing service credential")}
  if r.Header.Get("X-Testagram-Viewer-Authorization")!="Bearer v1.eyJ2IjoidjEiLCJpc3MiOiJ0ZXN0YWdyYW0tYmFja2VuZCIsImF1ZCI6InRlc3RhZ3JhbS1jZG4ifQ.signature"{t.Errorf("viewer identity not forwarded")}
  seen=true;w.Header().Set("X-Testagram-Visibility","private");w.Header().Set("Content-Type","image/webp");w.Write([]byte("private-bytes"))
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.OriginAuthToken="origin-secret"
 s:=New(cfg);rr:=httptest.NewRecorder();req:=httptest.NewRequest("GET","/v1/users/u1/private.webp",nil);req.Header.Set("Authorization","Bearer v1.eyJ2IjoidjEiLCJpc3MiOiJ0ZXN0YWdyYW0tYmFja2VuZCIsImF1ZCI6InRlc3RhZ3JhbS1jZG4ifQ.signature")
 s.Handler().ServeHTTP(rr,req)
 if rr.Code!=http.StatusOK||!seen{t.Fatalf("authorized request failed status=%d seen=%v body=%s",rr.Code,seen,rr.Body.String())}
 if rr.Header().Get("Cache-Control")!="private, no-store"||rr.Header().Get("X-Cache")!="BYPASS"{t.Fatalf("private cache policy incorrect: %+v",rr.Header())}
}

func TestPrivateMediaRangeHeadAndLargeStreaming(t *testing.T) {
 body:=strings.Repeat("x",2<<20)
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("X-Testagram-Visibility","private")
  if r.Header.Get("X-Testagram-Viewer-Authorization")!="Bearer assertion" { t.Errorf("viewer assertion not forwarded") }
  if r.Method==http.MethodHead {
   w.Header().Set("Content-Type","video/mp4")
   w.Header().Set("Content-Length",fmt.Sprint(len(body)))
   w.Header().Set("Accept-Ranges","bytes")
   w.Header().Set("ETag",`"media-v1"`)
   return
  }
  if r.Header.Get("Range")=="bytes=100-199" {
   if r.Header.Get("If-Range")!=`"media-v1"` { t.Errorf("If-Range not forwarded: %q",r.Header.Get("If-Range")) }
   w.Header().Set("Content-Type","video/mp4")
   w.Header().Set("Content-Length","100")
   w.Header().Set("Content-Range",fmt.Sprintf("bytes 100-199/%d",len(body)))
   w.Header().Set("Accept-Ranges","bytes")
   w.WriteHeader(http.StatusPartialContent)
   _,_=w.Write([]byte(body[100:200]))
   return
  }
  w.Header().Set("Content-Type","video/mp4")
  w.Header().Set("Content-Length",fmt.Sprint(len(body)))
  w.Header().Set("Accept-Ranges","bytes")
  _,_=w.Write([]byte(body))
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.MaxSegmentBytes=64
 s:=New(cfg)
 rangeReq:=httptest.NewRequest(http.MethodGet,"/v1/users/u1/video.mp4",nil)
 rangeReq.Header.Set("Authorization","Bearer assertion")
 rangeReq.Header.Set("Range","bytes=100-199")
 rangeReq.Header.Set("If-Range",`"media-v1"`)
 rangeRR:=httptest.NewRecorder();s.Handler().ServeHTTP(rangeRR,rangeReq)
 if rangeRR.Code!=http.StatusPartialContent { t.Fatalf("range status=%d body=%s",rangeRR.Code,rangeRR.Body.String()) }
 if rangeRR.Header().Get("Content-Range")!=fmt.Sprintf("bytes 100-199/%d",len(body)) || rangeRR.Header().Get("Accept-Ranges")!="bytes" || rangeRR.Header().Get("Cache-Control")!="private, no-store" { t.Fatalf("range headers not preserved: %+v",rangeRR.Header()) }
 if rangeRR.Body.Len()!=100 { t.Fatalf("range body bytes=%d, want 100",rangeRR.Body.Len()) }

 headReq:=httptest.NewRequest(http.MethodHead,"/v1/users/u1/video.mp4",nil);headReq.Header.Set("Authorization","Bearer assertion")
 headRR:=httptest.NewRecorder();s.Handler().ServeHTTP(headRR,headReq)
 if headRR.Code!=http.StatusOK || headRR.Body.Len()!=0 || headRR.Header().Get("Content-Length")!=fmt.Sprint(len(body)) || headRR.Header().Get("Accept-Ranges")!="bytes" { t.Fatalf("HEAD response status=%d bytes=%d headers=%+v",headRR.Code,headRR.Body.Len(),headRR.Header()) }

 fullReq:=httptest.NewRequest(http.MethodGet,"/v1/users/u1/video.mp4",nil);fullReq.Header.Set("Authorization","Bearer assertion")
 fullRR:=httptest.NewRecorder();s.Handler().ServeHTTP(fullRR,fullReq)
 if fullRR.Code!=http.StatusOK || fullRR.Body.Len()!=len(body) { t.Fatalf("large private stream status=%d bytes=%d, want %d",fullRR.Code,fullRR.Body.Len(),len(body)) }
}

func TestPrivateAuthorizationDenialCannotFailOverToAnotherOrigin(t *testing.T) {
 secondHit:=false
 denied:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"forbidden",http.StatusForbidden)}))
 defer denied.Close()
 permissive:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){secondHit=true;w.Write([]byte("must-not-leak"))}))
 defer permissive.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{denied.URL,permissive.URL};cfg.OriginAuthToken="origin-secret"
 s:=New(cfg);rr:=httptest.NewRecorder();req:=httptest.NewRequest("GET","/v1/users/u1/private.webp",nil);req.Header.Set("Authorization","Bearer v1.eyJ2IjoidjEiLCJpc3MiOiJ0ZXN0YWdyYW0tYmFja2VuZCIsImF1ZCI6InRlc3RhZ3JhbS1jZG4ifQ.signature")
 s.Handler().ServeHTTP(rr,req)
 if rr.Code!=http.StatusForbidden||secondHit{t.Fatalf("authorization denial failed open: status=%d secondOriginHit=%v",rr.Code,secondHit)}
}

func TestPublicAuthorizationDenialCannotFailOverToPublicCache(t *testing.T) {
 secondHit:=false
 denied:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"forbidden",http.StatusForbidden)}))
 defer denied.Close()
 permissive:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){secondHit=true;w.Header().Set("X-Testagram-Visibility","public");w.Write([]byte("must-not-leak"))}))
 defer permissive.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{denied.URL,permissive.URL}
 s:=New(cfg);rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/users/u1/private.webp",nil))
 if rr.Code!=http.StatusForbidden||secondHit{t.Fatalf("origin denial failed open: status=%d secondOriginHit=%v",rr.Code,secondHit)}
}

func TestUnknownVisibilityCannotFailOverToPublicOrigin(t *testing.T) {
 secondHit:=false
 unknown:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Write([]byte("unclassified"))}))
 defer unknown.Close()
 public:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){secondHit=true;w.Header().Set("X-Testagram-Visibility","public");w.Write([]byte("fallback"))}))
 defer public.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{unknown.URL,public.URL}
 s:=New(cfg);rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/users/u1/avatar.webp",nil))
 if rr.Code==http.StatusOK||secondHit{t.Fatalf("unknown visibility failed open: status=%d secondOriginHit=%v",rr.Code,secondHit)}
}

func TestViewerAuthorizationIsNotForwardedAcrossOriginRedirect(t *testing.T) {
 leaked:=false
 attacker:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){leaked=true;w.WriteHeader(http.StatusOK)}))
 defer attacker.Close()
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Redirect(w,r,attacker.URL+"/capture",http.StatusFound)}))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.OriginAuthToken="origin-secret"
 s:=New(cfg);rr:=httptest.NewRecorder();req:=httptest.NewRequest("GET","/v1/users/u1/private.webp",nil);req.Header.Set("Authorization","Bearer sensitive-appwrite-session")
 s.Handler().ServeHTTP(rr,req)
 if leaked {t.Fatal("viewer authorization followed an origin redirect to another host")}
 if rr.Code==http.StatusOK {t.Fatalf("redirect unexpectedly authorized request: %d",rr.Code)}
}

func TestReadyRequiresOriginServiceCredential(t *testing.T) {
 cfg:=testConfig(t);cfg.OriginAuthToken=""
 s:=New(cfg);rr:=httptest.NewRecorder()
 s.ready(rr,httptest.NewRequest(http.MethodGet,"/readyz",nil))
 if rr.Code!=http.StatusServiceUnavailable {t.Fatalf("ready status=%d body=%s",rr.Code,rr.Body.String())}
}

func TestPublicCacheRequiresExplicitOriginVisibility(t *testing.T) {
 for _,visibility:=range []string{"","private","public"} {
  origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if visibility!=""{w.Header().Set("X-Testagram-Visibility",visibility)};w.Write([]byte("asset"))}))
  cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};s:=New(cfg);rr:=httptest.NewRecorder()
  s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/public/a.webp",nil));origin.Close()
  if visibility=="public"{if rr.Code!=http.StatusOK{t.Fatalf("public object rejected: %d",rr.Code)}}else if rr.Code==http.StatusOK{t.Fatalf("origin visibility %q incorrectly served as public",visibility)}
 }
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
 if got := rr.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got,"If-Range") || !strings.Contains(got,"If-None-Match") || !strings.Contains(got,"If-Modified-Since") { t.Fatalf("conditional media request headers not allowed: %q",got) }
 if got := rr.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got,"Last-Modified") || !strings.Contains(got,"Cache-Status") { t.Fatalf("media response metadata not exposed: %q",got) }

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
  "testagram_edge_tv_prefetch_success_total 2",
  "testagram_edge_tv_prefetch_failures_total 1",
  "testagram_edge_tv_prefetch_warmed_seconds_total 30",
 } {
  if !strings.Contains(body, metric) { t.Errorf("metrics response missing %q", metric) }
 }
}

func TestHealthIdentifiesCDNService(t *testing.T) {
 s := New(testConfig(t))
 rr := httptest.NewRecorder()
 s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
 if rr.Code != http.StatusOK { t.Fatalf("health status=%d body=%s", rr.Code, rr.Body.String()) }
 var body map[string]any
 if err := json.NewDecoder(rr.Body).Decode(&body); err != nil { t.Fatal(err) }
 if body["ok"] != true || body["service"] != "testagram-cdn" { t.Fatalf("unexpected health contract: %#v", body) }
}

func TestTVPlaybackRouteIsDisabledUntilBackendGrantContractExists(t *testing.T) {
 s:=New(testConfig(t))
 rr:=httptest.NewRecorder()
 req:=httptest.NewRequest(http.MethodGet,"/v1/tv/channel-1/segment.ts?src=https%3A%2F%2Fexample.com%2Fseg.ts&token=legacy",nil)
 s.Handler().ServeHTTP(rr,req)
 if rr.Code!=http.StatusServiceUnavailable { t.Fatalf("TV route status=%d body=%s",rr.Code,rr.Body.String()) }
 if rr.Header().Get("Cache-Control")!="no-store" { t.Fatalf("TV disabled response cache policy=%q",rr.Header().Get("Cache-Control")) }
}

func TestPublicAuthorizationDenialDoesNotServeStaleCache(t *testing.T) {
 var denied atomic.Bool
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if denied.Load() { http.Error(w,"visibility revoked",http.StatusForbidden); return }
  w.Header().Set("X-Testagram-Visibility","public")
  _,_=w.Write([]byte("previously-public-bytes"))
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.SegmentTTL=15*time.Millisecond;cfg.StaleIfError=time.Minute
 s:=New(cfg)
 first:=httptest.NewRecorder()
 s.Handler().ServeHTTP(first,httptest.NewRequest(http.MethodGet,"/v1/public/logo.webp",nil))
 if first.Code!=http.StatusOK || first.Body.String()!="previously-public-bytes" { t.Fatalf("initial public response status=%d body=%q",first.Code,first.Body.String()) }
 time.Sleep(25*time.Millisecond)
 denied.Store(true)
 second:=httptest.NewRecorder()
 s.Handler().ServeHTTP(second,httptest.NewRequest(http.MethodGet,"/v1/public/logo.webp",nil))
 if second.Code!=http.StatusForbidden { t.Fatalf("revoked public object status=%d body=%q",second.Code,second.Body.String()) }
 if strings.Contains(second.Body.String(),"previously-public-bytes") { t.Fatal("stale bytes were served after authoritative visibility denial") }
}

func TestPublicOriginRedirectIsNotFollowed(t *testing.T) {
 leaked:=atomic.Bool{}
 attacker:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) { leaked.Store(true); w.WriteHeader(http.StatusOK) }))
 defer attacker.Close()
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Header.Get("X-Testagram-Origin-Auth")!="origin-secret" { t.Error("origin service credential missing from initial request") }
  http.Redirect(w,r,attacker.URL+"/capture",http.StatusFound)
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.OriginAuthToken="origin-secret"
 s:=New(cfg)
 rr:=httptest.NewRecorder()
 s.Handler().ServeHTTP(rr,httptest.NewRequest(http.MethodGet,"/v1/public/redirect.webp",nil))
 if leaked.Load() { t.Fatal("origin service credential was forwarded across redirect") }
 if rr.Code!=http.StatusBadGateway { t.Fatalf("redirect response status=%d",rr.Code) }
}

func TestPrivateOriginRequestIsCancelledWhenViewerDisconnects(t *testing.T) {
 started:=make(chan struct{})
 cancelled:=make(chan struct{})
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  close(started)
  <-r.Context().Done()
  close(cancelled)
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL}
 s:=New(cfg)
 ctx,cancel:=context.WithCancel(context.Background())
 req:=httptest.NewRequest(http.MethodGet,"/v1/users/u1/private.mp4",nil).WithContext(ctx)
 req.Header.Set("Authorization","Bearer viewer-assertion")
 done:=make(chan struct{})
 go func(){ defer close(done); s.Handler().ServeHTTP(httptest.NewRecorder(),req) }()
 select { case <-started: case <-time.After(time.Second): cancel(); t.Fatal("origin request did not start") }
 cancel()
 select { case <-cancelled: case <-time.After(time.Second): t.Fatal("origin stream was not cancelled with viewer request") }
 <-done
}

func TestReadyRequiresPrivateOriginEvenWhenShieldExists(t *testing.T) {
 cfg:=testConfig(t);cfg.OriginURLs=nil;cfg.ShieldURLs=[]string{"https://shield.example"};cfg.OriginAuthToken="origin-secret"
 s:=New(cfg);rr:=httptest.NewRecorder()
 s.ready(rr,httptest.NewRequest(http.MethodGet,"/readyz",nil))
 if rr.Code!=http.StatusServiceUnavailable { t.Fatalf("ready accepted shield-only config for private media: %d",rr.Code) }
}

func TestPublicDeliveryAlwaysConsultsBackendVisibility(t *testing.T) {
 var providerHits atomic.Int32
 provider:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  providerHits.Add(1)
  _,_=w.Write([]byte("provider-bypass"))
 }))
 defer provider.Close()
 backend:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.URL.Path!="/internal/cdn/v1/public/logo.webp" { t.Errorf("unexpected Backend origin path: %s",r.URL.Path) }
  w.Header().Set("X-Testagram-Visibility","public")
  _,_=w.Write([]byte("backend-authoritative"))
 }))
 defer backend.Close()
 cfg:=testConfig(t)
 cfg.OriginURLs=[]string{backend.URL}
 cfg.CloudinaryImageBaseURL=provider.URL
 cfg.R2PublicBaseURL=provider.URL
 s:=New(cfg)
 rr:=httptest.NewRecorder()
 s.Handler().ServeHTTP(rr,httptest.NewRequest(http.MethodGet,"/v1/public/logo.webp",nil))
 if rr.Code!=http.StatusOK || rr.Body.String()!="backend-authoritative" { t.Fatalf("public object did not come from Backend-classified origin: status=%d body=%q",rr.Code,rr.Body.String()) }
 if providerHits.Load()!=0 { t.Fatalf("direct public provider fallback bypassed Backend visibility: hits=%d",providerHits.Load()) }
}

func TestPrivateOriginMustExplicitlyClassifyResponse(t *testing.T) {
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  // A successful response without the Backend private classification is not
  // accepted, even when the viewer assertion and origin credentials exist.
  _,_=w.Write([]byte("unclassified-private-bytes"))
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL}
 s:=New(cfg);rr:=httptest.NewRecorder()
 req:=httptest.NewRequest(http.MethodGet,"/v1/users/u1/private.webp",nil)
 req.Header.Set("Authorization","Bearer viewer-assertion")
 s.Handler().ServeHTTP(rr,req)
 if rr.Code!=http.StatusForbidden { t.Fatalf("unclassified private origin response status=%d",rr.Code) }
 if strings.Contains(rr.Body.String(),"unclassified-private-bytes") { t.Fatal("unclassified bytes were delivered") }
}

func TestPublicMediaOriginOutageDoesNotServeStaleBytes(t *testing.T) {
 var unavailable atomic.Bool
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if unavailable.Load() { http.Error(w,"origin unavailable",http.StatusServiceUnavailable); return }
  w.Header().Set("X-Testagram-Visibility","public")
  _,_=w.Write([]byte("previously-public-bytes"))
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL};cfg.SegmentTTL=15*time.Millisecond;cfg.StaleIfError=time.Minute
 s:=New(cfg)
 first:=httptest.NewRecorder()
 s.Handler().ServeHTTP(first,httptest.NewRequest(http.MethodGet,"/v1/users/u1/public.webp",nil))
 if first.Code!=http.StatusOK || first.Body.String()!="previously-public-bytes" { t.Fatalf("initial public response status=%d body=%q",first.Code,first.Body.String()) }
 time.Sleep(25*time.Millisecond)
 unavailable.Store(true)
 second:=httptest.NewRecorder()
 s.Handler().ServeHTTP(second,httptest.NewRequest(http.MethodGet,"/v1/users/u1/public.webp",nil))
 if second.Code!=http.StatusBadGateway { t.Fatalf("origin outage status=%d",second.Code) }
 if strings.Contains(second.Body.String(),"previously-public-bytes") { t.Fatal("stale user media was served while Backend origin was unavailable") }
}

func TestPublicOriginRequestIsCancelledWhenViewerDisconnects(t *testing.T) {
 started:=make(chan struct{})
 cancelled:=make(chan struct{})
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  close(started)
  <-r.Context().Done()
  close(cancelled)
 }))
 defer origin.Close()
 cfg:=testConfig(t);cfg.OriginURLs=[]string{origin.URL}
 srv:=New(cfg)
 ctx,cancel:=context.WithCancel(context.Background())
 req:=httptest.NewRequest(http.MethodGet,"/v1/public/disconnect.webp",nil).WithContext(ctx)
 done:=make(chan struct{})
 go func(){ defer close(done);srv.Handler().ServeHTTP(httptest.NewRecorder(),req) }()
 select { case <-started: case <-time.After(time.Second): cancel();t.Fatal("public origin request did not start") }
 cancel()
 select { case <-cancelled: case <-time.After(time.Second):t.Fatal("public origin request was not cancelled") }
 <-done
}
