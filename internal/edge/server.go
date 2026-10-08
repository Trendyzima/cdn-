package edge

import (
 "crypto/hmac"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "bytes"
 "fmt"
 "hash/fnv"
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
 "github.com/Trendyzima/cdn-/internal/redis"
 "github.com/Trendyzima/cdn-/internal/config"
)

type Server struct {
 cfg config.Config
 cache *cache.Cache
 client *http.Client
 tvClient *http.Client
 hits,misses,upstream,inflight,served,rejected,staleHits atomic.Uint64
 mu sync.Mutex
 fetching map[string]*fetch
 originsMu sync.Mutex
 badUntil map[string]time.Time
 redis *redis.Client
 rateShards [32]rateShard
	tvFetching map[string]*tvFetch
}
type fetch struct{done chan struct{};data []byte;err error}
type tvFetch struct{done chan struct{};data []byte;contentType string;err error}
type bucket struct{tokens float64;last time.Time}
type rateShard struct{mu sync.Mutex;rates map[string]*bucket}

func New(cfg config.Config)*Server{
 c,e:=cache.NewWithHot(cfg.CacheDir,cfg.MaxCacheBytes,cfg.HotCacheBytes);if e!=nil{log.Fatalf("cache init: %v",e)}
 origins:=append([]string{},cfg.OriginURLs...);if cfg.OriginURL!=""{origins=append([]string{cfg.OriginURL},origins...)}
 cfg.OriginURLs=dedupe(origins);cfg.ShieldURLs=dedupe(cfg.ShieldURLs);cfg.EdgeURLs=dedupe(cfg.EdgeURLs)
 tr:=&http.Transport{
  MaxIdleConns: cfg.MaxIdleConns,
  MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
  MaxConnsPerHost: cfg.MaxConnsPerHost,
  IdleConnTimeout: 90 * time.Second,
  TLSHandshakeTimeout: 5 * time.Second,
  ResponseHeaderTimeout: cfg.OriginTimeout,
  ExpectContinueTimeout: 1 * time.Second,
  ForceAttemptHTTP2: true,
  DisableCompression: true,
  MaxResponseHeaderBytes: 64 << 10,
  WriteBufferSize: 64 << 10,
  ReadBufferSize: 64 << 10,
}
 tvTransport := tr.Clone()
tvTransport.DialContext = tvDialContext
tvClient := &http.Client{Transport: tvTransport, Timeout: cfg.OriginTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
  if req.URL.Scheme != "https" || isPrivateHost(req.URL.Hostname()) { return fmt.Errorf("unsafe TV redirect target") }
  if len(via) >= cfg.TVMaxRedirects { return fmt.Errorf("too many TV redirects") }
  return nil
}}
sv:=&Server{cfg:cfg,cache:c,redis:redis.New(cfg.UpstashRedisURL,cfg.UpstashRedisToken),client:&http.Client{Transport:tr,Timeout:cfg.OriginTimeout},tvClient:tvClient,fetching:map[string]*fetch{},tvFetching:map[string]*tvFetch{},badUntil:map[string]time.Time{}}
 for i:=range sv.rateShards{sv.rateShards[i].rates=make(map[string]*bucket)}
 return sv
}

func(s *Server)Handler()http.Handler{
 m:=http.NewServeMux()
 m.HandleFunc("/healthz",s.health);m.HandleFunc("/readyz",s.ready);m.HandleFunc("/metrics",s.metrics)
 m.HandleFunc("/route",s.route);m.HandleFunc("/api/cache/purge",s.purge);m.HandleFunc("/v1/media/url",s.mediaURL);m.HandleFunc("/api/media/url",s.mediaURL);m.HandleFunc("/v1/tv/",s.tvAsset);m.HandleFunc("/v1/",s.asset);m.HandleFunc("/media/",s.mediaAlias);m.HandleFunc("/users/",s.mediaAlias);m.HandleFunc("/profiles/",s.mediaAlias);m.HandleFunc("/uploads/",s.mediaAlias);m.HandleFunc("/avatars/",s.mediaAlias);m.HandleFunc("/covers/",s.mediaAlias);m.HandleFunc("/photos/",s.mediaAlias);m.HandleFunc("/videos/",s.mediaAlias)
 return s.cors(s.security(s.rateLimit(m)))
}
func(s *Server)health(w http.ResponseWriter,_ *http.Request){w.Header().Set("Content-Type","application/json");w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Cache-Control","no-store");items,bytes,capacity:=s.cache.Stats();hotBytes,hotCapacity:=s.cache.HotStats();_ = json.NewEncoder(w).Encode(map[string]any{"ok":true,"node_id":s.cfg.NodeID,"inflight":s.inflight.Load(),"cache_items":items,"cache_bytes":bytes,"cache_capacity":capacity,"hot_cache_bytes":hotBytes,"hot_cache_capacity":hotCapacity,"stale_hits":s.staleHits.Load()})}
func(s *Server)ready(w http.ResponseWriter,_ *http.Request){if len(s.cfg.OriginURLs)==0&&len(s.cfg.ShieldURLs)==0{http.Error(w,"upstream not configured",503);return};w.WriteHeader(200);_,_=w.Write([]byte("ready"))}
func(s *Server)metrics(w http.ResponseWriter,_ *http.Request){items,bytes,capacity:=s.cache.Stats();w.Header().Set("Content-Type","text/plain; version=0.0.4");fmt.Fprintf(w,"testagram_edge_cache_hits_total %d\n",s.hits.Load());fmt.Fprintf(w,"testagram_edge_cache_misses_total %d\n",s.misses.Load());fmt.Fprintf(w,"testagram_edge_upstream_requests_total %d\n",s.upstream.Load());fmt.Fprintf(w,"testagram_edge_inflight_requests %d\n",s.inflight.Load());fmt.Fprintf(w,"testagram_edge_bytes_served_total %d\n",s.served.Load());fmt.Fprintf(w,"testagram_edge_rejected_requests_total %d\n",s.rejected.Load());fmt.Fprintf(w,"testagram_edge_stale_hits_total %d\n",s.staleHits.Load());fmt.Fprintf(w,"testagram_edge_cache_items %d\n",items);fmt.Fprintf(w,"testagram_edge_cache_bytes %d\n",bytes);fmt.Fprintf(w,"testagram_edge_cache_capacity_bytes %d\n",capacity);hotBytes,hotCapacity:=s.cache.HotStats();fmt.Fprintf(w,"testagram_edge_hot_cache_bytes %d\n",hotBytes);fmt.Fprintf(w,"testagram_edge_hot_cache_capacity_bytes %d\n",hotCapacity)}

func(s *Server)route(w http.ResponseWriter,r *http.Request){
 if len(s.cfg.EdgeURLs)==0{http.Error(w,"edge routing not configured",503);return}
 key:=r.URL.Query().Get("key");if key==""{key=r.RemoteAddr}
 h:=sha256.Sum256([]byte(key));var n uint64;for _,v:=range h{n=(n<<5)^uint64(v)+(n>>2)}
 idx:=int(n%uint64(len(s.cfg.EdgeURLs)))
 w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"node_id":s.cfg.NodeID,"edge":s.cfg.EdgeURLs[idx],"index":idx})
}

func(s *Server)mediaURL(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
 raw:=strings.TrimSpace(r.URL.Query().Get("path"))
 if raw==""{http.Error(w,"path is required",400);return}
 rel,ok:=cleanAssetPath(raw)
 if !ok{http.Error(w,"invalid media path",400);return}
 base:=strings.TrimRight(s.cfg.PublicBaseURL,"/")
 if base==""{
  scheme:=r.Header.Get("X-Forwarded-Proto"); if scheme=="" { scheme="https" }
  base=scheme+"://"+r.Host
 }
 mediaURL:=base+strings.TrimRight(s.cfg.MediaURLPrefix,"/")+"/"+rel
 private:=r.URL.Query().Get("private")=="1"
 var expires any
 if private && s.cfg.PlaybackSecret!=""{
  exp:=time.Now().Add(10*time.Minute).Unix()
  expText:=strconv.FormatInt(exp,10);mediaURL+="?token="+url.QueryEscape(expText+"."+signToken(rel,s.cfg.PlaybackSecret,expText))
  expires=exp
 }
 w.Header().Set("Content-Type","application/json")
 _=json.NewEncoder(w).Encode(map[string]any{"url":mediaURL,"path":rel,"expires_at":expires,"public":!private})
}

func(s *Server)mediaAlias(w http.ResponseWriter,r *http.Request){
  // Backward-compatible public media URLs. Older Testagram rows use
  // https://media.testagram.site/users/... while the native CDN contract is
  // /v1/users/.... Keep one cache key and one origin contract so old posts do
  // not need a database rewrite before they can render.
  rr:=r.Clone(r.Context())
  if strings.HasPrefix(rr.URL.Path,"/media/"){ rr.URL.Path=strings.TrimPrefix(rr.URL.Path,"/media/") }
  rr.URL.Path="/v1/"+strings.TrimPrefix(rr.URL.Path,"/")
  s.asset(w,rr)
}

func(s *Server)asset(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet&&r.Method!=http.MethodHead{http.Error(w,"method not allowed",405);return}
 rel,ok:=cleanAssetPath(r.URL.Path);if !ok{s.rejected.Add(1);http.Error(w,"invalid path",400);return}
 token := strings.TrimSpace(r.URL.Query().Get("token"))
 if !s.authorized(rel,token){s.rejected.Add(1);http.Error(w,"unauthorized",401);return}
 private := token != ""
 if ok, status := validateRangeHeader(r.Header.Get("Range"), s.cfg.MaxSegmentBytes); !ok {
  s.rejected.Add(1)
  if status == http.StatusRequestedRangeNotSatisfiable { w.Header().Set("Content-Range","bytes */0") }
  http.Error(w,"invalid range",status)
  return
 }
 if e,data,err:=s.cache.GetHot(rel);err==nil{s.hits.Add(1);setType(w,rel);w.Header().Set("X-Cache","HOT");w.Header().Set("Cache-Status","testagram; hit; tier=hot");w.Header().Set("ETag",etagFile(e));if private { w.Header().Set("Cache-Control","private, no-store"); w.Header().Set("Vary","Authorization, Range") } else { w.Header().Set("Cache-Control",cacheControl(rel)) };w.Header().Set("Age",age(e)); if isLiveMedia(rel) { w.Header().Set("X-Accel-Buffering","no") }; s.serveBytes(w,r,data);return};if e,err:=s.cache.Get(rel);err==nil{s.hits.Add(1);setType(w,rel);w.Header().Set("X-Cache","HIT");w.Header().Set("Cache-Status","testagram; hit");w.Header().Set("ETag",etagFile(e));if private { w.Header().Set("Cache-Control","private, no-store"); w.Header().Set("Vary","Authorization, Range") } else { w.Header().Set("Cache-Control",cacheControl(rel)) };w.Header().Set("Age",age(e)); if isLiveMedia(rel) { w.Header().Set("X-Accel-Buffering","no") }; s.serveEntry(w,r,e);return}
 stale,staleErr:=s.cache.GetStale(rel);s.misses.Add(1)
 ttl:=s.cfg.SegmentTTL;if strings.HasSuffix(strings.ToLower(rel),".m3u8"){ttl=s.cfg.ManifestTTL}
 data,err:=s.fetchCoalesced(rel,ttl,s.cfg.StaleIfError)
 if err!=nil&&staleErr==nil{s.staleHits.Add(1);setType(w,rel);w.Header().Set("X-Cache","STALE");w.Header().Set("Cache-Status","testagram; stale-if-error");w.Header().Set("Warning","110 - Response is stale");w.Header().Set("Age",age(stale));if private { w.Header().Set("Cache-Control","private, no-store"); w.Header().Set("Vary","Authorization, Range") } else { w.Header().Set("Cache-Control",cacheControl(rel)) };s.serveEntry(w,r,stale);return}
 if err!=nil{http.Error(w,"upstream unavailable",502);return}
 setType(w,rel);w.Header().Set("X-Cache","MISS");w.Header().Set("Cache-Status","testagram; fwd=uri-miss");w.Header().Set("ETag","\""+hash(data)+"\"");if private { w.Header().Set("Cache-Control","private, no-store"); w.Header().Set("Vary","Authorization, Range") } else { w.Header().Set("Cache-Control",cacheControl(rel)) }; if isLiveMedia(rel) { w.Header().Set("X-Accel-Buffering","no") }; s.served.Add(uint64(len(data)));http.ServeContent(w,r,rel,time.Time{},bytes.NewReader(data))
}
func(s *Server)serveBytes(w http.ResponseWriter,r *http.Request,data []byte){http.ServeContent(w,r,"hot",time.Time{},bytes.NewReader(data))}

func(s *Server)serveEntry(w http.ResponseWriter,r *http.Request,e cache.Entry){
 f,err:=os.Open(e.Path);if err!=nil{http.Error(w,"cache object unavailable",502);return}
 defer f.Close()
 s.served.Add(uint64(e.Size))
 http.ServeContent(w,r,e.Key,e.ExpiresAt,f)
}

func(s *Server)fetchCoalesced(key string,ttl,staleFor time.Duration)([]byte,error){
 s.mu.Lock()
 if f,ok:=s.fetching[key];ok{s.mu.Unlock();<-f.done;return f.data,f.err}
 // A caller may have missed the cache before the current leader published it.
 // Recheck after acquiring the coalescing lock so late arrivals do not start
 // a second origin request.
 if e,err:=s.cache.Get(key);err==nil{
  s.mu.Unlock()
  data,readErr:=os.ReadFile(e.Path)
  return data,readErr
 }
 f:=&fetch{done:make(chan struct{})};s.fetching[key]=f;s.mu.Unlock()
 s.inflight.Add(1);s.upstream.Add(1);f.data,f.err=s.fetchUpstream(key);s.inflight.Add(^uint64(0))
 if f.err==nil{if _,e:=s.cache.Put(key,f.data,ttl,staleFor);e!=nil{log.Printf("cache put %s: %v",key,e)}}
 s.mu.Lock();close(f.done);delete(s.fetching,key);s.mu.Unlock();return f.data,f.err
}

func(s *Server)fetchUpstream(rel string)([]byte,error){
 if data,ok:=s.fetchCloudinary(rel);ok{return data,nil}
 if data,ok:=s.fetchR2(rel);ok{return data,nil}
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

func(s *Server)fetchR2(rel string)([]byte,bool){
 if s.cfg.R2PublicBaseURL=="" { return nil,false }
 u:=strings.TrimRight(s.cfg.R2PublicBaseURL,"/")+"/"+strings.TrimPrefix(rel,"/")
 req,e:=http.NewRequest(http.MethodGet,u,nil); if e!=nil{return nil,false}
 resp,e:=s.client.Do(req); if e!=nil||resp==nil{return nil,false}; defer resp.Body.Close()
 if resp.StatusCode<200||resp.StatusCode>=300{return nil,false}
 data,e:=io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1)); if e!=nil||int64(len(data))>s.cfg.MaxSegmentBytes{return nil,false}
 return data,true
}

func(s *Server)fetchCloudinary(rel string)([]byte,bool){
 base:=""
 l:=strings.ToLower(rel)
 if strings.HasSuffix(l,".jpg")||strings.HasSuffix(l,".jpeg")||strings.HasSuffix(l,".png")||strings.HasSuffix(l,".webp")||strings.HasSuffix(l,".avif")||strings.HasSuffix(l,".gif")||strings.HasSuffix(l,".svg"){base=s.cfg.CloudinaryImageBaseURL}
 if strings.HasSuffix(l,".mp4")||strings.HasSuffix(l,".webm")||strings.HasSuffix(l,".mov")||strings.HasSuffix(l,".m3u8")||strings.HasSuffix(l,".ts")||strings.HasSuffix(l,".m4s")||strings.HasSuffix(l,".mp3")||strings.HasSuffix(l,".aac"){base=s.cfg.CloudinaryVideoBaseURL}
 if base==""&&s.cfg.CloudinaryCloudName!=""&&(strings.HasSuffix(l,".jpg")||strings.HasSuffix(l,".jpeg")||strings.HasSuffix(l,".png")||strings.HasSuffix(l,".webp")||strings.HasSuffix(l,".avif")||strings.HasSuffix(l,".gif")||strings.HasSuffix(l,".svg")){base="https://res.cloudinary.com/"+url.PathEscape(s.cfg.CloudinaryCloudName)+"/image/upload"}
 if base==""{return nil,false}
 u:=strings.TrimRight(base,"/")+"/"+strings.TrimPrefix(rel,"/")
 req,e:=http.NewRequest(http.MethodGet,u,nil);if e!=nil{return nil,false}
 resp,e:=s.client.Do(req);if e!=nil||resp==nil{return nil,false};defer resp.Body.Close();if resp.StatusCode<200||resp.StatusCode>=300{return nil,false}
 data,e:=io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1));if e!=nil||int64(len(data))>s.cfg.MaxSegmentBytes{return nil,false};return data,true
}

func(s *Server)purge(w http.ResponseWriter,r *http.Request){if r.Method!=http.MethodPost&&r.Method!=http.MethodDelete{http.Error(w,"method not allowed",405);return};if s.cfg.PurgeToken==""||!hmac.Equal([]byte(r.Header.Get("Authorization")),[]byte("Bearer "+s.cfg.PurgeToken)){http.Error(w,"unauthorized",401);return};key:=strings.TrimSpace(r.URL.Query().Get("path"));if key==""{s.cache.Clear();w.WriteHeader(204);return};rel,ok:=cleanAssetPath("/v1/"+key);if !ok{http.Error(w,"invalid path",400);return};if !s.cache.Delete(rel){w.WriteHeader(404);return};w.WriteHeader(204)}


func signToken(rel, secret, exp string) string {
 mac := hmac.New(sha256.New, []byte(secret))
 _, _ = mac.Write([]byte(rel + "|" + exp))
 return hex.EncodeToString(mac.Sum(nil))
}
func(s *Server)authorized(rel,token string)bool{if s.cfg.PlaybackSecret==""{return true};parts:=strings.Split(token,".");if len(parts)!=2{return false};exp,e:=strconv.ParseInt(parts[0],10,64);if e!=nil||exp<time.Now().Unix(){return false};mac:=hmac.New(sha256.New,[]byte(s.cfg.PlaybackSecret));_,_=mac.Write([]byte(rel+"|"+parts[0]));expected:=hex.EncodeToString(mac.Sum(nil));return hmac.Equal([]byte(expected),[]byte(parts[1]))}
func(s *Server)originBlocked(o string)bool{s.originsMu.Lock();defer s.originsMu.Unlock();return time.Now().Before(s.badUntil[o])}
func(s *Server)blockOrigin(o string){s.originsMu.Lock();s.badUntil[o]=time.Now().Add(5*time.Second);s.originsMu.Unlock()}
func(s *Server)clearOrigin(o string){s.originsMu.Lock();delete(s.badUntil,o);s.originsMu.Unlock()}

func(s *Server)rateLimit(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 if s.cfg.RateLimitPerMin<=0||s.cfg.RateLimitBurst<=0{next.ServeHTTP(w,r);return}
 ip,_,e:=net.SplitHostPort(r.RemoteAddr);if e!=nil{ip=r.RemoteAddr};if s.cfg.TrustCloudflare { if cf:=strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf!="" { ip=cf } };now:=time.Now();rate:=float64(s.cfg.RateLimitPerMin)/60
 h:=fnv.New32a();_,_=h.Write([]byte(ip));sh:=&s.rateShards[h.Sum32()%uint32(len(s.rateShards))]
 sh.mu.Lock()
 if len(sh.rates)>4000{for k,b:=range sh.rates{if now.Sub(b.last)>time.Minute{delete(sh.rates,k)}}}
 if len(sh.rates)>5000{sh.mu.Unlock();s.rejected.Add(1);http.Error(w,"rate limiter overloaded",503);return}
 b:=sh.rates[ip];if b==nil{b=&bucket{tokens:float64(s.cfg.RateLimitBurst),last:now};sh.rates[ip]=b}
 b.tokens+=now.Sub(b.last).Seconds()*rate;if b.tokens>float64(s.cfg.RateLimitBurst){b.tokens=float64(s.cfg.RateLimitBurst)};b.last=now;allowed:=b.tokens>=1;if allowed{b.tokens--};sh.mu.Unlock()
 if !allowed{s.rejected.Add(1);w.Header().Set("Retry-After","1");http.Error(w,"rate limit exceeded",429);return};next.ServeHTTP(w,r)
})}

func dedupe(in []string)[]string{seen:=map[string]bool{};out:=[]string{};for _,x:=range in{if x!=""&&!seen[x]{seen[x]=true;out=append(out,x)}};return out}
func cleanAssetPath(p string)(string,bool){
 p=strings.TrimPrefix(p,"/v1/")
 if p==""||strings.ContainsAny(p,"\\%") { return "",false }
 for _,r:=range p { if r<0x20 || r==0x7f { return "",false } }
 for _,segment:=range strings.Split(p,"/"){ if segment==""||segment=="."||segment==".." { return "",false } }
 c:=path.Clean("/"+p)
 if c=="/"||strings.HasPrefix(c,"/../"){return "",false}
 rel:=strings.TrimPrefix(c,"/")
 root:=strings.SplitN(rel,"/",2)[0]
 switch root {
 case "users","profiles","uploads","avatars","covers","photos","videos","media","live":
 default: return "",false
 }
 return rel,true
}
func validateRangeHeader(v string,max int64)(bool,int){
 if strings.TrimSpace(v)=="" { return true,http.StatusOK }
 v=strings.TrimSpace(v)
 if !strings.HasPrefix(v,"bytes=") { return false,http.StatusRequestedRangeNotSatisfiable }
 spec:=strings.TrimSpace(strings.TrimPrefix(v,"bytes="))
 if spec==""||strings.Contains(spec,",") { return false,http.StatusRequestedRangeNotSatisfiable }
 parts:=strings.SplitN(spec,"-",2)
 if len(parts)!=2||parts[0]==""&&parts[1]=="" { return false,http.StatusRequestedRangeNotSatisfiable }
 parse:=func(x string)(int64,bool){ n,e:=strconv.ParseInt(x,10,64); return n,e==nil&&n>=0 }
 if parts[0]=="" {
  n,ok:=parse(parts[1]); if !ok||n==0||n>max{return false,http.StatusRequestedRangeNotSatisfiable}; return true,http.StatusOK
 }
 start,ok:=parse(parts[0]); if !ok||start>=max{return false,http.StatusRequestedRangeNotSatisfiable}
 if parts[1]=="" { return true,http.StatusOK }
 end,ok:=parse(parts[1]); if !ok||end<start{return false,http.StatusRequestedRangeNotSatisfiable}
 if end-start+1>max{return false,http.StatusRequestedRangeNotSatisfiable}
 return true,http.StatusOK
}
func setType(w http.ResponseWriter,rel string){
 l:=strings.ToLower(rel)
 switch{
 case strings.HasSuffix(l,".m3u8"):w.Header().Set("Content-Type","application/vnd.apple.mpegurl")
 case strings.HasSuffix(l,".ts"):w.Header().Set("Content-Type","video/mp2t")
 case strings.HasSuffix(l,".m4s"):w.Header().Set("Content-Type","video/iso.segment")
 case strings.HasSuffix(l,".mp4"):w.Header().Set("Content-Type","video/mp4")
 case strings.HasSuffix(l,".webm"):w.Header().Set("Content-Type","video/webm")
 case strings.HasSuffix(l,".mov"):w.Header().Set("Content-Type","video/quicktime")
 case strings.HasSuffix(l,".aac"):w.Header().Set("Content-Type","audio/aac")
 case strings.HasSuffix(l,".mp3"):w.Header().Set("Content-Type","audio/mpeg")
 case strings.HasSuffix(l,".wav"):w.Header().Set("Content-Type","audio/wav")
 case strings.HasSuffix(l,".vtt"):w.Header().Set("Content-Type","text/vtt")
 case strings.HasSuffix(l,".jpg")||strings.HasSuffix(l,".jpeg"):w.Header().Set("Content-Type","image/jpeg")
 case strings.HasSuffix(l,".png"):w.Header().Set("Content-Type","image/png")
 case strings.HasSuffix(l,".webp"):w.Header().Set("Content-Type","image/webp")
 case strings.HasSuffix(l,".avif"):w.Header().Set("Content-Type","image/avif")
 case strings.HasSuffix(l,".gif"):w.Header().Set("Content-Type","image/gif")
 case strings.HasSuffix(l,".svg"):w.Header().Set("Content-Type","image/svg+xml")
 default:w.Header().Set("Content-Type","application/octet-stream")
 }
}
func isLiveMedia(rel string) bool { l:=strings.ToLower(rel); return strings.HasSuffix(l,".m3u8")||strings.HasSuffix(l,".ts")||strings.HasSuffix(l,".m4s") }
func cacheControl(rel string)string{
 l:=strings.ToLower(rel)
 if strings.HasSuffix(l,".m3u8"){return "public, max-age=1, s-maxage=1, stale-while-revalidate=2, stale-if-error=30"}
 if strings.HasSuffix(l,".ts")||strings.HasSuffix(l,".m4s"){return "public, max-age=15, s-maxage=20, stale-while-revalidate=30, stale-if-error=30"}
 if strings.HasSuffix(l,".jpg")||strings.HasSuffix(l,".jpeg")||strings.HasSuffix(l,".png")||strings.HasSuffix(l,".webp")||strings.HasSuffix(l,".avif")||strings.HasSuffix(l,".gif"){
  return "public, max-age=31536000, s-maxage=31536000, immutable, stale-if-error=86400"
 }
 if strings.HasSuffix(l,".mp4")||strings.HasSuffix(l,".webm")||strings.HasSuffix(l,".mov")||strings.HasSuffix(l,".mp3")||strings.HasSuffix(l,".aac")||strings.HasSuffix(l,".wav"){
  return "public, max-age=86400, s-maxage=604800, stale-while-revalidate=86400, stale-if-error=86400"
 }
 return "public, max-age=15, s-maxage=20, stale-while-revalidate=30, stale-if-error=30"
}
func etagFile(e cache.Entry)string{return "\""+fmt.Sprintf("%x-%x",e.Size,e.CreatedAt.UnixNano())+"\""}
func hash(b []byte)string{h:=sha256.Sum256(b);return hex.EncodeToString(h[:])[:16]}
func age(e cache.Entry)string{a:=time.Since(e.CreatedAt);if a<0{return "0"};return strconv.FormatInt(int64(a/time.Second),10)}
func(s *Server)cors(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){o:=r.Header.Get("Origin");allowed:=len(s.cfg.AllowedOrigins)==0;for _,a:=range s.cfg.AllowedOrigins{if o==a{allowed=true;break}};if allowed&&o!=""{w.Header().Set("Access-Control-Allow-Origin",o);w.Header().Set("Vary","Origin")};if r.Method=="OPTIONS"{w.Header().Set("Access-Control-Allow-Methods","GET,HEAD,OPTIONS");w.Header().Set("Access-Control-Allow-Headers","Range,Content-Type");w.WriteHeader(204);return};next.ServeHTTP(w,r)})}
func(s *Server)security(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("X-Frame-Options","DENY");next.ServeHTTP(w,r)})}
