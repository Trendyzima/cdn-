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
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){calls.Add(1);time.Sleep(30*time.Millisecond);_,_=w.Write([]byte("segment-data"))}))
 defer origin.Close()
 cfg:=config.Config{CacheDir:t.TempDir(),MaxCacheBytes:1<<20,SegmentTTL:time.Minute,ManifestTTL:time.Second,StaleIfError:time.Minute,MaxSegmentBytes:1<<20,OriginURLs:[]string{origin.URL},NodeID:"test",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second,MaxIdleConns:100,MaxIdleConnsPerHost:100}
 s:=New(cfg);var wg sync.WaitGroup
 for i:=0;i<20;i++{wg.Add(1);go func(){defer wg.Done();rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/live/a.ts",nil));if rr.Code!=200{t.Errorf("status %d",rr.Code)}}()}
 wg.Wait();if got:=calls.Load();got!=1{t.Fatalf("expected one origin call, got %d",got)}
}

func TestOriginFailover(t *testing.T){
 bad:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"bad",500)}));defer bad.Close()
 good:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_,_=w.Write([]byte("ok"))}));defer good.Close()
 cfg:=config.Config{CacheDir:t.TempDir(),MaxCacheBytes:1<<20,SegmentTTL:time.Minute,ManifestTTL:time.Second,StaleIfError:time.Minute,MaxSegmentBytes:1<<20,OriginURLs:[]string{bad.URL,good.URL},NodeID:"test",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second,MaxIdleConns:100,MaxIdleConnsPerHost:100}
 s:=New(cfg);rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/live/a.ts",nil))
 if rr.Code!=200||rr.Body.String()!="ok"{t.Fatalf("failover: status=%d body=%q",rr.Code,rr.Body.String())}
}

func TestStaleIfOriginFails(t *testing.T){
 var fail atomic.Bool
 origin:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if fail.Load(){http.Error(w,"down",500);return};_,_=w.Write([]byte("stable-segment"))}))
 defer origin.Close()
 cfg:=config.Config{CacheDir:t.TempDir(),MaxCacheBytes:1<<20,SegmentTTL:20*time.Millisecond,ManifestTTL:10*time.Millisecond,StaleIfError:time.Second,MaxSegmentBytes:1<<20,OriginURLs:[]string{origin.URL},NodeID:"test",RateLimitPerMin:100000,RateLimitBurst:1000,OriginTimeout:time.Second,MaxIdleConns:100,MaxIdleConnsPerHost:100}
 s:=New(cfg)
 rr:=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/live/a.ts",nil));if rr.Code!=200||rr.Body.String()!="stable-segment"{t.Fatalf("initial: %d %q",rr.Code,rr.Body.String())}
 time.Sleep(30*time.Millisecond);fail.Store(true)
 rr=httptest.NewRecorder();s.Handler().ServeHTTP(rr,httptest.NewRequest("GET","/v1/live/a.ts",nil));if rr.Code!=200||rr.Body.String()!="stable-segment"{t.Fatalf("stale: %d %q",rr.Code,rr.Body.String())}
 if s.staleHits.Load()!=1{t.Fatalf("expected one stale hit, got %d",s.staleHits.Load())}
}

func TestEdgeRouteDeterministic(t *testing.T){
 s:=New(config.Config{CacheDir:t.TempDir(),MaxCacheBytes:1<<20,EdgeURLs:[]string{"https://a.example","https://b.example","https://c.example"},RateLimitPerMin:100000,RateLimitBurst:1000})
 a:=httptest.NewRecorder();b:=httptest.NewRecorder();req:=httptest.NewRequest("GET","/route?key=user-123",nil);s.route(a,req);s.route(b,req)
 if a.Body.String()!=b.Body.String(){t.Fatal("route is not deterministic")}
}
