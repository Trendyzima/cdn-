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
 hits,misses,upstream,inflight atomic.Uint64
 mu sync.Mutex
 fetching map[string]*fetch
 originsMu sync.Mutex
 badUntil map[string]time.Time
}
type fetch struct{done chan struct{};data []byte;err error}

func New(cfg config.Config)*Server{
 c,e:=cache.New(cfg.CacheDir,cfg.MaxCacheBytes);if e!=nil{log.Fatalf("cache init: %v",e)}
 origins:=append([]string{},cfg.OriginURLs...);if cfg.OriginURL!=""{origins=append([]string{cfg.OriginURL},origins...)}
 cfg.OriginURLs=dedupe(origins)
 return &Server{cfg:cfg,cache:c,client:&http.Client{Timeout:45*time.Second},fetching:map[string]*fetch{},badUntil:map[string]time.Time{}}
}
func(s *Server)Handler()http.Handler{
 m:=http.NewServeMux();m.HandleFunc("/healthz",s.health);m.HandleFunc("/readyz",s.ready);m.HandleFunc("/metrics",s.metrics);m.HandleFunc("/v1/",s.asset)
 return s.cors(s.security(m))
}
func(s *Server)health(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"ok":true,"node_id":s.cfg.NodeID,"inflight":s.inflight.Load()})}
func(s *Server)ready(w http.ResponseWriter,_ *http.Request){if len(s.cfg.OriginURLs)==0{http.Error(w,"origin not configured",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready"))}
func(s *Server)metrics(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","text/plain; version=0.0.4");fmt.Fprintf(w,"testagram_edge_cache_hits_total %d\n",s.hits.Load());fmt.Fprintf(w,"testagram_edge_cache_misses_total %d\n",s.misses.Load());fmt.Fprintf(w,"testagram_edge_upstream_requests_total %d\n",s.upstream.Load());fmt.Fprintf(w,"testagram_edge_inflight_requests %d\n",s.inflight.Load())}
func(s *Server)asset(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet&&r.Method!=http.MethodHead{http.Error(w,"method not allowed",405);return}
 rel,ok:=cleanAssetPath(r.URL.Path);if !ok{http.Error(w,"invalid path",400);return}
 if e,e2:=s.cache.Get(rel);e2==nil{s.hits.Add(1);setType(w,rel);w.Header().Set("Cache-Control","public, max-age=2, stale-while-revalidate=10");http.ServeFile(w,r,e.Path);return}
 s.misses.Add(1);data,e:=s.fetchCoalesced(rel);if e!=nil{http.Error(w,"upstream unavailable",502);return}
 ttl:=s.cfg.SegmentTTL;if strings.HasSuffix(strings.ToLower(rel),".m3u8"){ttl=s.cfg.ManifestTTL}
 if _,e=s.cache.Put(rel,data,ttl);e!=nil{log.Printf("cache put %s: %v",rel,e)}
 setType(w,rel);w.Header().Set("Cache-Control","public, max-age=2, stale-while-revalidate=10");w.Header().Set("ETag","\""+hash(data)+"\"");http.ServeContent(w,r,rel,time.Time{},bytesReader{b:data})
}
func(s *Server)fetchCoalesced(key string)([]byte,error){
 s.mu.Lock();if f,ok:=s.fetching[key];ok{s.mu.Unlock();<-f.done;return f.data,f.err};f:=&fetch{done:make(chan struct{})};s.fetching[key]=f;s.mu.Unlock()
 s.inflight.Add(1);s.upstream.Add(1);f.data,f.err=s.fetchOrigin(key);s.inflight.Add(^uint64(0))
 s.mu.Lock();close(f.done);delete(s.fetching,key);s.mu.Unlock();return f.data,f.err
}
func(s *Server)fetchOrigin(rel string)([]byte,error){
 for _,baseURL:=range s.cfg.OriginURLs{
  if s.originBlocked(baseURL){continue}
  b,e:=url.Parse(baseURL);if e!=nil{continue};b.Path=path.Join(b.Path,"v1",rel)
  req,e:=http.NewRequest(http.MethodGet,b.String(),nil);if e!=nil{continue};req.Header.Set("X-Testagram-Edge",s.cfg.NodeID)
  resp,e:=s.client.Do(req);if e!=nil{s.blockOrigin(baseURL);continue}
  if resp.StatusCode<200||resp.StatusCode>=300{resp.Body.Close();s.blockOrigin(baseURL);continue}
  data,e:=io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1));resp.Body.Close();if e!=nil{continue}
  if int64(len(data))>s.cfg.MaxSegmentBytes{continue};s.clearOrigin(baseURL);return data,nil
 }
 return nil,fmt.Errorf("all origins unavailable")
}
func(s *Server)originBlocked(o string)bool{s.originsMu.Lock();defer s.originsMu.Unlock();return time.Now().Before(s.badUntil[o])}
func(s *Server)blockOrigin(o string){s.originsMu.Lock();s.badUntil[o]=time.Now().Add(5*time.Second);s.originsMu.Unlock()}
func(s *Server)clearOrigin(o string){s.originsMu.Lock();delete(s.badUntil,o);s.originsMu.Unlock()}
func dedupe(in []string)[]string{seen:=map[string]bool{};out:=[]string{};for _,x:=range in{if x!=""&&!seen[x]{seen[x]=true;out=append(out,x)}};return out}
type bytesReader struct{b []byte;i int64}
func(r bytesReader)Read(p []byte)(int,error){if r.i>=int64(len(r.b)){return 0,io.EOF};n:=copy(p,r.b[r.i:]);r.i+=int64(n);return n,nil}
func(r bytesReader)Seek(o int64,w int)(int64,error){var n int64;switch w{case io.SeekStart:n=o;case io.SeekCurrent:n=r.i+o;case io.SeekEnd:n=int64(len(r.b))+o;default:return 0,fmt.Errorf("bad seek")};if n<0{return 0,fmt.Errorf("negative seek")};r.i=n;return n,nil}
func cleanAssetPath(p string)(string,bool){p=strings.TrimPrefix(p,"/v1/");if p==""||strings.Contains(p,"\\"){return "",false};c:=path.Clean("/"+p);if c=="/"||strings.HasPrefix(c,"/../")||strings.Contains(c,"/../"){return "",false};return strings.TrimPrefix(c,"/"),true}
func setType(w http.ResponseWriter,rel string){l:=strings.ToLower(rel);switch{case strings.HasSuffix(l,".m3u8"):w.Header().Set("Content-Type","application/vnd.apple.mpegurl");case strings.HasSuffix(l,".ts"):w.Header().Set("Content-Type","video/mp2t");case strings.HasSuffix(l,".m4s"):w.Header().Set("Content-Type","video/iso.segment");case strings.HasSuffix(l,".mp4"):w.Header().Set("Content-Type","video/mp4");default:w.Header().Set("Content-Type","application/octet-stream")}}
func hash(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])[:16]}
func(s *Server)cors(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){o:=r.Header.Get("Origin");allowed:=len(s.cfg.AllowedOrigins)==0;for _,a:=range s.cfg.AllowedOrigins{if o==a{allowed=true;break}};if allowed&&o!=""{w.Header().Set("Access-Control-Allow-Origin",o);w.Header().Set("Vary","Origin")};if r.Method=="OPTIONS"{w.Header().Set("Access-Control-Allow-Methods","GET,HEAD,OPTIONS");w.Header().Set("Access-Control-Allow-Headers","Range,Content-Type");w.WriteHeader(204);return};next.ServeHTTP(w,r)})}
func(s *Server)security(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer");next.ServeHTTP(w,r)})}
