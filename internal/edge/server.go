package edge

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "log"
 "net/http"
 "net/url"
 "path"
 "strings"
 "sync"
 "sync/atomic"
 "time"
 "github.com/Trendyzima/cdn-/internal/cache"
 "github.com/Trendyzima/cdn-/internal/config"
)

type Server struct {
 cfg config.Config
 cache *cache.Cache
 client *http.Client
 hits, misses, upstream, inflight atomic.Uint64
 mu sync.Mutex
 fetching map[string]*fetch
}
type fetch struct { done chan struct{}; data []byte; err error }

func New(cfg config.Config) *Server {
 c, err := cache.New(cfg.CacheDir, cfg.MaxCacheBytes)
 if err != nil { log.Fatalf("cache init: %v", err) }
 return &Server{cfg:cfg,cache:c,client:&http.Client{Timeout:45*time.Second},fetching:map[string]*fetch{}}
}
func (s *Server) Handler() http.Handler {
 mux:=http.NewServeMux()
 mux.HandleFunc("/healthz",s.health); mux.HandleFunc("/readyz",s.ready); mux.HandleFunc("/metrics",s.metrics); mux.HandleFunc("/v1/",s.asset)
 return s.cors(s.security(mux))
}
func (s *Server) health(w http.ResponseWriter,_ *http.Request) {
 w.Header().Set("Content-Type","application/json")
 _=json.NewEncoder(w).Encode(map[string]any{"ok":true,"node_id":s.cfg.NodeID,"inflight":s.inflight.Load()})
}
func (s *Server) ready(w http.ResponseWriter,_ *http.Request) {
 if s.cfg.OriginURL=="" {http.Error(w,"origin not configured",503);return}
 w.WriteHeader(200);_,_=w.Write([]byte("ready"))
}
func (s *Server) metrics(w http.ResponseWriter,_ *http.Request) {
 w.Header().Set("Content-Type","text/plain; version=0.0.4")
 fmt.Fprintf(w,"testagram_edge_cache_hits_total %d\n",s.hits.Load())
 fmt.Fprintf(w,"testagram_edge_cache_misses_total %d\n",s.misses.Load())
 fmt.Fprintf(w,"testagram_edge_upstream_requests_total %d\n",s.upstream.Load())
 fmt.Fprintf(w,"testagram_edge_inflight_requests %d\n",s.inflight.Load())
}
func (s *Server) asset(w http.ResponseWriter,r *http.Request) {
 if r.Method!=http.MethodGet && r.Method!=http.MethodHead {http.Error(w,"method not allowed",405);return}
 if s.cfg.OriginURL=="" {http.Error(w,"origin not configured",503);return}
 rel,ok:=cleanAssetPath(r.URL.Path);if !ok {http.Error(w,"invalid path",400);return}
 if e,err:=s.cache.Get(rel);err==nil {
  s.hits.Add(1);setType(w,rel);http.ServeFile(w,r,e.Path);return
 }
 s.misses.Add(1)
 data,err:=s.fetchCoalesced(rel);if err!=nil {http.Error(w,"upstream unavailable",502);return}
 ttl:=s.cfg.SegmentTTL;if strings.HasSuffix(strings.ToLower(rel),".m3u8"){ttl=s.cfg.ManifestTTL}
 if _,err=s.cache.Put(rel,data,ttl);err!=nil {log.Printf("cache put %s: %v",rel,err)}
 setType(w,rel);w.Header().Set("Cache-Control","public, max-age=2, stale-while-revalidate=10");w.Header().Set("ETag","\""+hash(data)+"\"")
 http.ServeContent(w,r,rel,time.Time{},bytesReader{b:data})
}
func (s *Server) fetchCoalesced(key string)([]byte,error) {
 s.mu.Lock()
 if f,ok:=s.fetching[key];ok{s.mu.Unlock();<-f.done;return f.data,f.err}
 f:=&fetch{done:make(chan struct{})};s.fetching[key]=f;s.mu.Unlock()
 s.inflight.Add(1);s.upstream.Add(1);f.data,f.err=s.fetchOrigin(key);s.inflight.Add(^uint64(0))
 s.mu.Lock();close(f.done);delete(s.fetching,key);s.mu.Unlock()
 return f.data,f.err
}
func (s *Server) fetchOrigin(rel string)([]byte,error) {
 base,err:=url.Parse(s.cfg.OriginURL);if err!=nil{return nil,err}
 base.Path=path.Join(base.Path,"v1",rel)
 req,err:=http.NewRequest(http.MethodGet,base.String(),nil);if err!=nil{return nil,err}
 req.Header.Set("X-Testagram-Edge",s.cfg.NodeID)
 resp,err:=s.client.Do(req);if err!=nil{return nil,err};defer resp.Body.Close()
 if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("origin status %d",resp.StatusCode)}
 b,err:=io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1));if err!=nil{return nil,err}
 if int64(len(b))>s.cfg.MaxSegmentBytes{return nil,fmt.Errorf("object too large")}
 return b,nil
}
type bytesReader struct{b []byte;i int64}
func(r bytesReader)Read(p []byte)(int,error){if r.i>=int64(len(r.b)){return 0,io.EOF};n:=copy(p,r.b[r.i:]);r.i+=int64(n);return n,nil}
func(r bytesReader)Seek(o int64,w int)(int64,error){var n int64;switch w{case io.SeekStart:n=o;case io.SeekCurrent:n=r.i+o;case io.SeekEnd:n=int64(len(r.b))+o;default:return 0,fmt.Errorf("bad seek")};if n<0{return 0,fmt.Errorf("negative seek")};r.i=n;return n,nil}
func cleanAssetPath(p string)(string,bool){p=strings.TrimPrefix(p,"/v1/");if p==""||strings.Contains(p,"\\"){return "",false};c:=path.Clean("/"+p);if c=="/"||strings.HasPrefix(c,"/../")||strings.Contains(c,"/../"){return "",false};return strings.TrimPrefix(c,"/"),true}
func setType(w http.ResponseWriter,rel string){l:=strings.ToLower(rel);switch{case strings.HasSuffix(l,".m3u8"):w.Header().Set("Content-Type","application/vnd.apple.mpegurl");case strings.HasSuffix(l,".ts"):w.Header().Set("Content-Type","video/mp2t");case strings.HasSuffix(l,".m4s"):w.Header().Set("Content-Type","video/iso.segment");case strings.HasSuffix(l,".mp4"):w.Header().Set("Content-Type","video/mp4");default:w.Header().Set("Content-Type","application/octet-stream")}}
func hash(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])[:16]}
func(s *Server)cors(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){o:=r.Header.Get("Origin");allowed:=len(s.cfg.AllowedOrigins)==0;for _,a:=range s.cfg.AllowedOrigins{if o==a{allowed=true;break}};if allowed&&o!=""{w.Header().Set("Access-Control-Allow-Origin",o);w.Header().Set("Vary","Origin")};if r.Method=="OPTIONS"{w.Header().Set("Access-Control-Allow-Methods","GET,HEAD,OPTIONS");w.Header().Set("Access-Control-Allow-Headers","Range,Content-Type");w.WriteHeader(204);return};next.ServeHTTP(w,r)})}
func(s *Server)security(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer");next.ServeHTTP(w,r)})}
