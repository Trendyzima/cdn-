package edge

import (
 "net/http"
 "net/http/httptest"
 "sync"
 "sync/atomic"
 "testing"
 "time"
 "github.com/Trendyzima/cdn-/internal/config"
)

func TestRequestCoalescing(t *testing.T){
 var calls atomic.Int64
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  calls.Add(1)
  time.Sleep(30*time.Millisecond)
  _,_=w.Write([]byte("segment-data"))
 }))
 defer origin.Close()
 cfg:=config.Config{CacheDir:t.TempDir(),MaxCacheBytes:1<<20,SegmentTTL:time.Minute,ManifestTTL:time.Second,MaxSegmentBytes:1<<20,OriginURLs:[]string{origin.URL},NodeID:"test",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second}
 s:=New(cfg)
 var wg sync.WaitGroup
 for i:=0;i<20;i++{
  wg.Add(1)
  go func(){defer wg.Done();rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/live/a.ts",nil));if rr.Code!=200{t.Errorf("status %d",rr.Code)}}()
 }
 wg.Wait()
 if got:=calls.Load();got!=1{t.Fatalf("expected one origin call, got %d",got)}
}
