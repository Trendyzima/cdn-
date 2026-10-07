package edge

import (
 "crypto/hmac"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "log"
 "net"
 "net/http"
 "net/url"
 "os"
 "path"
 "strconv"
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
 hits,misses,upstream,inflight,served,rejected,staleHits atomic.Uint64
 mu sync.Mutex
 fetching map[string]*fetch
 originsMu sync.Mutex
 badUntil map[string]time.Time
 rateMu sync.Mutex
 rates map[string]*bucket
}
type fetch struct{done chan struct{};data []byte;err error}
type bucket struct{tokens float64;last time.Time}

func New(cfg config.Config)*Server{
 c,e:=cache.New(cfg.CacheDir,cfg.MaxCacheBytes);if e!=nil{log.Fatalf("cache init: %v",e)}
 origins:=append([]string{},cfg.OriginURLs...);if cfg.OriginURL!=""{origins=append([]string{cfg.OriginURL},origins...)}
 cfg.OriginURLs=dedupe(origins);cfg.ShieldURLs=dedupe(cfg.ShieldURLs);cfg.EdgeURLs=dedupe(cfg.EdgeURLs)
 tr:=&http.Transport{MaxIdleConns:cfg.MaxIdleConns,MaxIdleConnsPerHost:cfg.MaxIdleConnsPerHost,MaxConnsPerHost:cfg.MaxConnsPerHost,IdleConnTimeout:90*time.Second,TLSHandshakeTimeout:5*time.Second,ResponseHeaderTimeout:cfg.OriginTimeout,ExpectContinueTimeout:1*time.Second}
 return &Server{cfg:cfg,cache:c,client:&http.Client{Transport:tr,Timeout:cfg.OriginTimeout},fetching:map[string]*fetch{},badUntil:map[string]time.Time{},rates:map[string]*bucket{}}
}

func(s *Server)Handler()http.Handler{
 m:=http.NewServeMux()
 m.HandleFunc("/healthz",s.health);m.HandleFunc("/readyz",s.ready);m.HandleFunc("/metrics",s.metrics)
 m.HandleFunc("/route",s.route);m.HandleFunc("/api/cache/purge",s.purge);m.HandleFunc("/v1/",s.asset)
 return s.cors(s.security(s.rateLimit(m)))
}
func(s *Server)health(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","application/json");items,bytes,capacity:=s.cache.Stats();_ = json.NewEncoder(w).Encode(map[string]any{"ok":true,"node_id":s.cfg.NodeID,"inflight":s.inflight.Load(),"cache_items":items,"cache_bytes":bytes,"cache_capacity":capacity,"stale_hits":s.staleHits.Load()})}
func(s *Server)ready(w http.ResponseWriter,_ *http.Request){if len(s.cfg.OriginURLs)==0&&len(s.cfg.ShieldURLs)==0{http.Error(w,"upstream not configured",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready"))}
func(s *Server)metrics(w http.ResponseWriter,_ *http.Request){items,bytes,capacity:=s.cache.Stats();w.Header().Set("Content-Type","text/plain; version=0.0.4");fmt.Fprintf(w,"testagram_edge_cache_hits_total %d\n",s.hits.Load());fmt.Fprintf(w,"testagram_edge_cache_misses_total %d\n",s.misses.Load());fmt.Fprintf(w,"testagram_edge_upstream_requests_total %d\n",s.upstream.Load());fmt.Fprintf(w,"testagram_edge_inflight_requests %d\n",s.inflight.Load());fmt.Fprintf(w,"testagram_edge_bytes_served_total %d\n",s.served.Load());fmt.Fprintf(w,"testagram_edge_rejected_requests_total %d\n",s.rejected.Load());fmt.Fprintf(w,"testagram_edge_stale_hits_total %d\n",s.staleHits.Load());fmt.Fprintf(w,"testagram_edge_cache_items %d\n",items);fmt.Fprintf(w,"testagram_edge_cache_bytes %d\n",bytes);fmt.Fprintf(w,"testagram_edge_cache_capacity_bytes %d\n",capacity)}

func(s *Server)route(w http.ResponseWriter,r *http.Request){
 if len(s.cfg.EdgeURLs)==0{http.Error(w,"edge routing not configured",503);return}
 key:=r.URL.Query().Get("key");if key==""{key=r.RemoteAddr}
 h:=sha256.Sum256([]byte(key));var n uint64;for _,v:=range h{n=(n<<5)^uint64(v)+(n>>2)}
 idx:=int(n%uint64(len(s.cfg.EdgeURLs)))
 w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"node_id":s.cfg.NodeID,"edge":s.cfg.EdgeURLs[idx],"index":idx})
}

func(s *Server)asset(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet&&r.Method!=http.MethodHead{http.Error(w,"method not allowed",405);return}
 rel,ok:=cleanAssetPath(r.URL.Path);if !ok{s.rejected.Add(1);http.Error(w,"invalid path",400);return}
 if !s.authorized(rel,r.URL.Query().Get("token")){s.rejected.Add(1);http.Error(w,"unauthorized",401);return}
 if e,err:=s.cache.Get(rel);err==nil{s.hits.Add(1);setType(w,rel);w.Header().Set("X-Cache","HIT");w.Header().Set("Cache-Status","testagram; hit");w.Header().Set("ETag",etagFile(e));w.Header().Set("Cache-Control",cacheControl(rel));s.serveEntry(w,r,e);return}
 stale,staleErr:=s.cache.GetStale(rel);s.misses.Add(1)
 ttl:=s.cfg.SegmentTTL;if strings.HasSuffix(strings.ToLower(rel),".m3u8"){ttl=s.cfg.ManifestTTL}
 data,err:=s.fetchCoalesced(rel,ttl,s.cfg.StaleIfError)
 if err!=nil&&staleErr==nil{s.staleHits.Add(1);setType(w,rel);w.Header().Set("X-Cache","STALE");w.Header().Set("Cache-Status","testagram; stale-if-error");w.Header().Set("Warning","110 - Response is stale");w.Header().Set("Age",age(stale));s.serveEntry(w,r,stale);return}
 if err!=nil{http.Error(w,"upstream unavailable",502);return}
 setType(w,rel);w.Header().Set("X-Cache","MISS");w.Header().Set("Cache-Status","testagram; fwd=uri-miss");w.Header().Set("ETag","\""+hash(data)+"\"");w.Header().Set("Cache-Control",cacheControl(rel));s.served.Add(uint64(len(data)));http.ServeContent(w,r,rel,time.Time{},bytesReader{b:data})
}
func(s *Server)serveEntry(w http.ResponseWriter,r *http.Request,e cache.Entry){if st,err:=os.Stat(e.Path);err==nil{s.served.Add(uint64(st.Size()))};http.ServeFile(w,r,e.Path)}

func(s *Server)fetchCoalesced(key string,ttl,staleFor time.Duration)([]byte,error){
 s.mu.Lock();if f,ok:=s.fetching[key];ok{s.mu.Unlock();<-f.done;return f.data,f.err}
 f:=&fetch{done:make(chan struct{})};s.fetching[key]=f;s.mu.Unlock()
 s.inflight.Add(1);s.upstream.Add(1);f.data,f.err=s.fetchUpstream(key);s.inflight.Add(^uint64(0))
 if f.err==nil{if _,e:=s.cache.Put(key,f.data,ttl,staleFor);e!=nil{log.Printf("cache put %s: %v",key,e)}}
 s.mu.Lock();close(f.done);delete(s.fetching,key);s.mu.Unlock();return f.data,f.err
}

func(s *Server)fetchUpstream(rel string)([]byte,error){
 bases:=append([]string{},s.cfg.ShieldURLs...);bases=append(bases,s.cfg.OriginURLs...)
 var lastErr error
 for _,baseURL:=range bases{
  if s.originBlocked(baseURL){continue}
  b,e:=url.Parse(baseURL);if e!=nil{lastErr=e;continue}
  b.Path=path.Join(b.Path,"v1",rel)
  req,e:=http.NewRequest(http.MethodGet,b.String(),nil);if e!=nil{lastErr=e;continue}
  req.Header.Set("X-Testagram-Edge",s.cfg.NodeID)
  if s.cfg.OriginAuthToken!=""{req.Header.Set("Authorization","Bearer "+s.cfg.OriginAuthToken)}
  resp,e:=s.client.Do(req)
  if e!=nil{s.blockOrigin(baseURL);lastErr=e;continue}
  if resp.StatusCode==404||resp.StatusCode==410{resp.Body.Close();lastErr=fmt.Errorf("upstream returned %d",resp.StatusCode);continue}
  if resp.StatusCode<200||resp.StatusCode>=300{resp.Body.Close();s.blockOrigin(baseURL);lastErr=fmt.Errorf("upstream returned %d",resp.StatusCode);continue}
  data,e:=io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1));resp.Body.Close()
  if e!=nil{s.blockOrigin(baseURL);lastErr=e;continue}
  if int64(len(data))>s.cfg.MaxSegmentBytes{s.blockOrigin(baseURL);lastErr=fmt.Errorf("object exceeds max size");continue}
  s.clearOrigin(baseURL);return data,nil
 }
 if lastErr==nil{lastErr=fmt.Errorf("all upstreams unavailable")}
 return nil,lastErr
}

func(s *Server)purge(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost&&r.Method!=http.MethodDelete{http.Error(w,"method not allowed",405);return};if s.cfg.PurgeToken==""||!hmac.Equal([]byte(r.Header.Get("Authorization")),[]byte("Bearer "+s.cfg.PurgeToken)){http.Error(w,"unauthorized",401);return};key:=strings.TrimSpace(r.URL.Query().Get("path"));if key==""{s.cache.Clear();w.WriteHeader(204);return};rel,ok:=cleanAssetPath("/v1/"+key);if !ok{http.Error(w,"invalid path",400);return};if !s.cache.Delete(rel){w.WriteHeader(404);return};w.WriteHeader(204)}

func(s *Server)authorized(rel,token string)bool{if s.cfg.PlaybackSecret==""{return true};parts:=strings.Split(token,".");if len(parts)!=2{return false};exp,e:=strconv.ParseInt(parts[0],10,64);if e!=nil||exp<time.Now().Unix(){return false};mac:=hmac.New(sha256.New,[]byte(s.cfg.PlaybackSecret));_,_=mac.Write([]byte(rel+"|"+parts[0]));expected:=hex.EncodeToString(mac.Sum(nil));return hmac.Equal([]byte(expected),[]byte(parts[1]))}
func(s *Server)originBlocked(o string)bool{s.originsMu.Lock();defer s.originsMu.Unlock();return time.Now().Before(s.badUntil[o])}
func(s *Server)blockOrigin(o string){s.originsMu.Lock();s.badUntil[o]=time.Now().Add(5*time.Second);s.originsMu.Unlock()}
func(s *Server)clearOrigin(o string){s.originsMu.Lock();delete(s.badUntil,o);s.originsMu.Unlock()}

func(s *Server)rateLimit(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 if s.cfg.RateLimitPerMin<=0||s.cfg.RateLimitBurst<=0{next.ServeHTTP(w,r);return}
 ip,_,e:=net.SplitHostPort(r.RemoteAddr);if e!=nil{ip=r.RemoteAddr};now:=time.Now();rate:=float64(s.cfg.RateLimitPerMin)/60
 s.rateMu.Lock()
 if len(s.rates)>100000{for k,b:=range s.rates{if now.Sub(b.last)>time.Minute{delete(s.rates,k)}}}
 if len(s.rates)>120000{s.rateMu.Unlock();s.rejected.Add(1);http.Error(w,"rate limiter overloaded",503);return}
 b:=s.rates[ip];if b==nil{b=&bucket{tokens:float64(s.cfg.RateLimitBurst),last:now};s.rates[ip]=b}
 b.tokens+=now.Sub(b.last).Seconds()*rate;if b.tokens>float64(s.cfg.RateLimitBurst){b.tokens=float64(s.cfg.RateLimitBurst)};b.last=now;allowed:=b.tokens>=1;if allowed{b.tokens--};s.rateMu.Unlock()
 if !allowed{s.rejected.Add(1);w.Header().Set("Retry-After","1");http.Error(w,"rate limit exceeded",429);return};next.ServeHTTP(w,r)
})}

func dedupe(in []string)[]string{seen:=map[string]bool{};out:=[]string{};for _,x:=range in{if x!=""&&!seen[x]{seen[x]=true;out=append(out,x)}};return out}
type bytesReader struct{b []byte;i int64}
func(r bytesReader)Read(p []byte)(int,error){if r.i>=int64(len(r.b)){return 0,io.EOF};n:=copy(p,r.b[r.i:]);r.i+=int64(n);return n,nil}
func(r bytesReader)Seek(o int64,w int)(int64,error){var n int64;switch w{case io.SeekStart:n=o;case io.SeekCurrent:n=r.i+o;case io.SeekEnd:n=int64(len(r.b))+o;default:return 0,fmt.Errorf("bad seek")};if n<0{return 0,fmt.Errorf("negative seek")};r.i=n;return n,nil}
func cleanAssetPath(p string)(string,bool){p=strings.TrimPrefix(p,"/v1/");if p==""||strings.Contains(p,"\\"){return "",false};for _,segment:=range strings.Split(p,"/"){if segment==".."{return "",false}};c:=path.Clean("/"+p);if c=="/"||strings.HasPrefix(c,"/../"){return "",false};return strings.TrimPrefix(c,"/"),true}
func setType(w http.ResponseWriter,rel string){l:=strings.ToLower(rel);switch{case strings.HasSuffix(l,".m3u8"):w.Header().Set("Content-Type","application/vnd.apple.mpegurl");case strings.HasSuffix(l,".ts"):w.Header().Set("Content-Type","video/mp2t");case strings.HasSuffix(l,".m4s"):w.Header().Set("Content-Type","video/iso.segment");case strings.HasSuffix(l,".mp4"):w.Header().Set("Content-Type","video/mp4");case strings.HasSuffix(l,".aac"):w.Header().Set("Content-Type","audio/aac");case strings.HasSuffix(l,".mp3"):w.Header().Set("Content-Type","audio/mpeg");case strings.HasSuffix(l,".vtt"):w.Header().Set("Content-Type","text/vtt");default:w.Header().Set("Content-Type","application/octet-stream")}}
func cacheControl(rel string)string{if strings.HasSuffix(strings.ToLower(rel),".m3u8"){return "public, max-age=1, s-maxage=1, stale-while-revalidate=2, stale-if-error=30"};return "public, max-age=15, s-maxage=20, stale-while-revalidate=30, stale-if-error=30"}
func etagFile(e cache.Entry)string{return "\""+fmt.Sprintf("%x-%x",e.Size,e.ExpiresAt.UnixNano())+"\""}
func hash(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])[:16]}
func age(e cache.Entry)string{a:=time.Since(e.ExpiresAt);if a<0{return "0"};return strconv.FormatInt(int64(a/time.Second),10)}
func(s *Server)cors(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){o:=r.Header.Get("Origin");allowed:=len(s.cfg.AllowedOrigins)==0;for _,a:=range s.cfg.AllowedOrigins{if o==a{allowed=true;break}};if allowed&&o!=""{w.Header().Set("Access-Control-Allow-Origin",o);w.Header().Set("Vary","Origin")};if r.Method=="OPTIONS"{w.Header().Set("Access-Control-Allow-Methods","GET,HEAD,OPTIONS");w.Header().Set("Access-Control-Allow-Headers","Range,Content-Type");w.WriteHeader(204);return};next.ServeHTTP(w,r)})}
func(s *Server)security(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("X-Frame-Options","DENY");next.ServeHTTP(w,r)})}
