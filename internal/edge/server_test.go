package edge

import (
 "crypto/hmac"
 "crypto/sha256"
 "encoding/hex"
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
 }
 for _,tt:=range tests{got,ok:=cleanAssetPath(tt.in);if ok!=tt.ok||got!=tt.want{t.Fatalf("%q => %q,%v",tt.in,got,ok)}}
}
func TestPlaybackToken(t *testing.T){
 s:=&Server{cfg:config.Config{PlaybackSecret:"secret"}}
 token:=signToken("live/a.ts","secret","9999999999")
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
func signToken(rel,secret,exp string)string{
 mac:=hmac.New(sha256.New,[]byte(secret));_,_=mac.Write([]byte(rel+"|"+exp))
 return exp+"."+hex.EncodeToString(mac.Sum(nil))
}

func TestMediaCachePolicy(t *testing.T) {
 if got:=cacheControl("users/a/thumbnail.webp"); !strings.Contains(got, "immutable") { t.Fatalf("image cache policy not immutable: %s", got) }
 if got:=cacheControl("live/channel/seg-1.m4s"); !strings.Contains(got, "s-maxage=20") { t.Fatalf("segment cache policy changed unexpectedly: %s", got) }
 if got:=cacheControl("live/channel/index.m3u8"); !strings.Contains(got, "max-age=1") { t.Fatalf("manifest cache policy changed unexpectedly: %s", got) }
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
 cfg.MediaURLPrefix = "/media"
 s := New(cfg)
 rr := httptest.NewRecorder()
 s.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/v1/media/url?path=users/u1/avatar.webp", nil))
 if rr.Code != 200 { t.Fatalf("media URL status=%d", rr.Code) }
 if !strings.Contains(rr.Body.String(), "https://media.testagram.site/media/users/u1/avatar.webp") {
  t.Fatalf("unexpected media URL: %s", rr.Body.String())
 }
}
