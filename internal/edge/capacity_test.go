package edge

import (
 "net/http"
 "net/http/httptest"
 "os"
 "sync"
 "sync/atomic"
 "testing"
 "time"

 "github.com/Trendyzima/cdn-/internal/config"
)

// CAPACITY_TEST=1 enables the 100k logical-viewer test. It is intentionally
// opt-in because CI runners are verification machines, not production load generators.
func TestCapacity100kLogicalViewers(t *testing.T) {
 if os.Getenv("CAPACITY_TEST") != "1" { t.Skip("set CAPACITY_TEST=1 to run the 100k capacity test") }

 var originCalls atomic.Int64
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  originCalls.Add(1)
  w.Header().Set("Content-Type","video/mp2t")
  _,_=w.Write(make([]byte,1024))
 }))
 defer origin.Close()

 cfg:=config.Config{
  CacheDir:t.TempDir(),MaxCacheBytes:64<<20,SegmentTTL:time.Minute,ManifestTTL:time.Second,
  StaleIfError:time.Minute,MaxSegmentBytes:1<<20,OriginURLs:[]string{origin.URL},
  NodeID:"capacity-test",RateLimitPerMin:1000000,RateLimitBurst:100000,
  OriginTimeout:time.Second,MaxIdleConns:4096,MaxIdleConnsPerHost:4096,
 }
 s:=New(cfg)
 h:=s.Handler()
 const viewers=100000
 var wg sync.WaitGroup
 var ok atomic.Int64
 wg.Add(viewers)
 for i:=0;i<viewers;i++{
  go func(){defer wg.Done();rr:=httptest.NewRecorder();h.ServeHTTP(rr,httptest.NewRequest(http.MethodGet,"/v1/live/capacity.ts",nil));if rr.Code==http.StatusOK&&rr.Body.Len()==1024{ok.Add(1)}}()
 }
 wg.Wait()
 if got:=ok.Load();got!=viewers{t.Fatalf("100k viewer simulation: %d/%d succeeded",got,viewers)}
 if got:=originCalls.Load();got!=1{t.Fatalf("cache fanout regression: expected 1 origin fetch, got %d",got)}
}
