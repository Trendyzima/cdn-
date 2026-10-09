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

// CAPACITY_TEST=1 enables the one-million logical-viewer test. It is intentionally
// opt-in because CI runners are verification machines, not production load generators.
func TestCapacity1MLogicalViewers(t *testing.T) {
 if os.Getenv("CAPACITY_TEST") != "1" { t.Skip("set CAPACITY_TEST=1 to run the one-million logical-viewer test") }

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
  NodeID:"capacity-test",RateLimitPerMin:0,RateLimitBurst:0,
  OriginTimeout:time.Second,MaxIdleConns:4096,MaxIdleConnsPerHost:4096,
 }
 s:=New(cfg)
 h:=http.HandlerFunc(s.asset)
 const viewers=1000000
 const workers=4096
 jobs:=make(chan struct{},workers)
 var wg sync.WaitGroup
 var ok atomic.Int64
 var failures atomic.Int64
 var firstFailure atomic.Int64
 for w:=0;w<workers;w++{
  wg.Add(1)
  go func(){
   defer wg.Done()
   for range jobs{
    rr:=httptest.NewRecorder()
    h.ServeHTTP(rr,httptest.NewRequest(http.MethodGet,"/v1/live/capacity.ts",nil))
    if rr.Code==http.StatusOK&&rr.Body.Len()==1024{ok.Add(1)}else{if failures.Add(1)==1{firstFailure.Store(int64(rr.Code))}}
   }
  }()
 }
 for i:=0;i<viewers;i++{jobs<-struct{}{}}
 close(jobs)
 wg.Wait()
 if got:=ok.Load();got!=viewers{t.Fatalf("one-million logical viewer simulation: %d/%d succeeded; first failure HTTP status=%d; failures=%d",got,viewers,firstFailure.Load(),failures.Load())}
 if got:=originCalls.Load();got!=1{t.Fatalf("cache fanout regression: expected 1 origin fetch, got %d",got)}
}
