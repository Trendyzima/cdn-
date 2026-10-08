package edge

import (
  "compress/gzip"
  "context"
  "crypto/hmac"
  "crypto/sha256"
  "encoding/base64"
  "encoding/hex"
  "fmt"
  "io"
  "net"
  "net/http"
  "net/url"
  "os"
  "path"
  "regexp"
  "strconv"
  "sync"
  "strings"
  "time"

  "github.com/Trendyzima/cdn-/internal/config"
)

var tvURIAttribute = regexp.MustCompile(`URI="([^"]+)"`)

func (s *Server) tvAsset(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet && r.Method != http.MethodHead { http.Error(w,"method not allowed",405); return }
  rel, ok := cleanAssetPath(strings.TrimPrefix(r.URL.Path,"/v1/"))
  if !ok { s.rejected.Add(1); http.Error(w,"invalid path",400); return }
  parts := strings.Split(rel,"/")
  if len(parts) < 3 || parts[0] != "tv" { http.Error(w,"invalid tv path",400); return }
  scope := parts[1]
  src := strings.TrimSpace(r.URL.Query().Get("src"))
  token := strings.TrimSpace(r.URL.Query().Get("token"))
  target, err := url.Parse(src)
  if err != nil || target.Scheme != "https" || isPrivateHost(target.Hostname()) { http.Error(w,"invalid stream source",400); return }
  if !s.authorizedTV(scope,src,token) { s.rejected.Add(1); http.Error(w,"unauthorized",401); return }
  directives := tvDeliveryDirectives(r.URL.Query(), strings.HasSuffix(strings.ToLower(target.Path),".m3u8"))
  fetchTarget := target
  if len(directives) > 0 { fetchTarget = cloneURLWithQuery(target,directives) }

  cacheKey := tvCacheKey(scope, fetchTarget, strings.Join(parts[2:],"/"))
  if e, data, err := s.cache.GetHot(cacheKey); err == nil {s.hits.Add(1);w.Header().Set("X-Cache","HOT");w.Header().Set("Cache-Status","testagram; hit; tier=hot");w.Header().Set("Content-Type",func()string{if strings.HasSuffix(strings.ToLower(rel),".m3u8"){return "application/vnd.apple.mpegurl; charset=utf-8"};return "application/octet-stream"}());w.Header().Set("Cache-Control",tvCacheControl(rel,len(directives)>0));w.Header().Set("ETag",etagFile(e));w.Header().Set("Age",age(e));if isPlaylistResponse(w.Header().Get("Content-Type"),data)&&len(data)>=512&&acceptsGzip(r){w.Header().Set("Content-Encoding","gzip");w.Header().Set("X-Accel-Buffering","no");gw,_:=gzip.NewWriterLevel(w,gzip.BestSpeed);w.WriteHeader(200);_,_=gw.Write(data);_=gw.Close();return};s.serveBytes(w,r,data);return};if e, err := s.cache.Get(cacheKey); err == nil {
    s.hits.Add(1); w.Header().Set("X-Cache","HIT"); w.Header().Set("Cache-Status","testagram; hit")
    setType(w,rel); w.Header().Set("Cache-Control",tvCacheControl(rel,len(tvDeliveryDirectives(r.URL.Query(),strings.HasSuffix(strings.ToLower(target.Path),".m3u8")))>0)); w.Header().Set("ETag",etagFile(e)); w.Header().Set("Age",age(e)); if strings.HasSuffix(strings.ToLower(rel),".m3u8") { w.Header().Set("Vary","Accept-Encoding") }
    s.serveEntry(w,r,e); return
  }

  stale, staleErr := s.cache.GetStale(cacheKey)
  s.misses.Add(1)
  data, contentType, fetchErr := s.fetchTVCoalesced(cacheKey,fetchTarget,scope,rel)
  if fetchErr != nil {
    if staleErr == nil {
      s.staleHits.Add(1); w.Header().Set("X-Cache","STALE"); w.Header().Set("Cache-Status","testagram; stale-if-error")
      w.Header().Set("Warning","110 - Response is stale"); w.Header().Set("Age",age(stale)); setType(w,rel); w.Header().Set("Cache-Control",tvCacheControl(rel,len(directives)>0)); s.serveEntry(w,r,stale); return
    }
    http.Error(w,"upstream unavailable",502); return
  }

  w.Header().Set("X-Cache","MISS"); w.Header().Set("Cache-Status","testagram; fwd=uri-miss")
  w.Header().Set("Content-Type",contentType); if isPlaylistResponse(contentType,data) { w.Header().Set("X-Accel-Buffering","no") }; w.Header().Set("Cache-Control",tvCacheControl(rel,len(directives)>0)); w.Header().Set("ETag","\""+hash(data)+"\"")
  if isPlaylistResponse(contentType,data) { w.Header().Set("Vary","Accept-Encoding") }
  s.served.Add(uint64(len(data)))
  if r.Method == http.MethodHead { w.WriteHeader(200); return }
  if isPlaylistResponse(contentType,data) && len(data)>=512 && acceptsGzip(r) {
    w.Header().Set("Content-Encoding","gzip")
    w.Header().Set("X-Accel-Buffering","no"); gw,_:=gzip.NewWriterLevel(w,gzip.BestSpeed); w.WriteHeader(200); _,_=gw.Write(data); _=gw.Close(); return
  }
  w.WriteHeader(200); _,_ = w.Write(data)
}

func (s *Server) fetchTVCoalesced(key string, target *url.URL, scope, rel string) ([]byte,string,error) {
  s.mu.Lock()
  if f,ok := s.tvFetching[key]; ok { s.mu.Unlock(); <-f.done; return f.data,f.contentType,f.err }
  if e,err := s.cache.Get(key); err == nil { s.mu.Unlock(); data,readErr := os.ReadFile(e.Path); ct := "application/octet-stream"; if strings.HasSuffix(strings.ToLower(rel),".m3u8") { ct="application/vnd.apple.mpegurl; charset=utf-8" }; return data,ct,readErr }
  f := &tvFetch{done:make(chan struct{})}; s.tvFetching[key]=f; s.mu.Unlock()

  s.inflight.Add(1); s.upstream.Add(1)
  f.data,f.contentType,f.err = s.fetchTVSource(target)
  if f.err == nil && isHLSContent(target,f.contentType,f.data) {
    f.data = s.rewriteTVPlaylist(scope,target,f.data)
    f.contentType = "application/vnd.apple.mpegurl; charset=utf-8"
  }
  if f.err == nil {
    ttl := ttlForTV(s.cfg,rel)
    if _,e := s.cache.Put(key,f.data,ttl,s.cfg.StaleIfError); e != nil { f.err=e }
    if f.err == nil && isHLSContent(target,f.contentType,f.data) { s.prefetchTVSegments(scope,target,f.data) }
  }
  s.inflight.Add(^uint64(0))
  s.mu.Lock(); close(f.done); delete(s.tvFetching,key); s.mu.Unlock()
  return f.data,f.contentType,f.err
}

func ttlForTV(cfg config.Config, rel string) time.Duration {
  if strings.HasSuffix(strings.ToLower(rel),".m3u8") { return cfg.ManifestTTL }
  return cfg.SegmentTTL
}

func tvCacheKey(scope string, target *url.URL, name string) string {
  return "tv/" + scope + "/" + hashString(target.String()) + "/" + name
}

// Prefetch a rolling window of upcoming live media. We warm enough advertised
// segments to cover the configured window (45s by default), then continue warming
// on each playlist refresh. The playlist itself stays short-lived and is never
// cached for the duration of the media window.
func parseTVPrefetchCandidates(scope string, playlistURL *url.URL, data []byte, target time.Duration) []struct { target *url.URL; key, name string; duration time.Duration } {
	lines := strings.Split(string(data), "\n")
	out := make([]struct { target *url.URL; key, name string; duration time.Duration }, 0, 16)
	var duration, total time.Duration
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#EXT-X-MAP:") && strings.Contains(line, "URI=") {
			if strings.HasPrefix(line, "#EXT-X-MAP:") { duration = 0 }
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			value := strings.TrimPrefix(line, "#EXTINF:")
			if comma := strings.IndexByte(value, ','); comma >= 0 { value = value[:comma] }
			if seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && seconds > 0 && seconds < 120 {
				duration = time.Duration(seconds * float64(time.Second))
			} else { duration = 0 }
			continue
		}
		if strings.HasPrefix(line, "#") { continue }
		resolved, err := playlistURL.Parse(line)
		if err != nil || resolved.Scheme != "https" || isPrivateHost(resolved.Hostname()) { duration = 0; continue }
		name := path.Base(resolved.Path)
		if name == "." || name == "/" || name == "" { name = "segment" }
		lower := strings.ToLower(resolved.Path)
		// HLS media URIs are not required to carry .ts/.m4s extensions. Exclude
		// playlists by path/content convention, but allow extensionless segments.
		if strings.HasSuffix(lower, ".m3u8") || strings.HasSuffix(lower, ".mpd") { duration = 0; continue }
		out = append(out, struct { target *url.URL; key, name string; duration time.Duration }{
			target: resolved, key: tvCacheKey(scope, resolved, name), name: name, duration: duration,
		})
		if duration > 0 { total += duration }
		duration = 0
		if target > 0 && total >= target { break }
	}
	return out
}

func (s *Server) prefetchTVSegments(scope string, playlistURL *url.URL, data []byte) {
	if s.cfg.TVPrefetchSeconds <= 0 { return }
	candidates := parseTVPrefetchCandidates(scope, playlistURL, data, s.cfg.TVPrefetchSeconds)
	if len(candidates) == 0 { return }

	limit := s.cfg.TVPrefetchConcurrency
	if limit < 1 { limit = 1 }
	if limit > 4 { limit = 4 }
	warmupCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	jobs := make(chan struct { target *url.URL; key, name string; duration time.Duration })
	var wg sync.WaitGroup
	workers := limit
	if len(candidates) < workers { workers = len(candidates) }
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-warmupCtx.Done(): return
				case item, ok := <-jobs:
					if !ok { return }
					if _, err := s.cache.Get(item.key); err == nil {
						s.tvPrefetchHits.Add(1)
						continue
					}
					s.tvPrefetchMisses.Add(1)
					if _, _, err := s.fetchTVCoalesced(item.key, item.target, scope, item.name); err != nil {
						s.tvPrefetchFailures.Add(1)
					} else {
						s.tvPrefetchSuccesses.Add(1)
						s.tvPrefetchWarmedSeconds.Add(uint64(item.duration / time.Second))
					}
				}
			}()
	}
send:
	for _, item := range candidates {
		select {
		case <-warmupCtx.Done(): break send
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
}
func (s *Server) fetchTVSource(target *url.URL) ([]byte,string,error) {
  req,err := http.NewRequest(http.MethodGet,target.String(),nil); if err != nil { return nil,"",err }
  req.Header.Set("User-Agent","TestagramEdge/2.0")
  req.Header.Set("Accept","application/vnd.apple.mpegurl,application/x-mpegURL,video/*,audio/*,*/*")
  resp,err := s.tvClient.Do(req); if err != nil { return nil,"",err }
  defer resp.Body.Close()
  if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil,"",fmt.Errorf("upstream returned %d",resp.StatusCode) }
  data,err := io.ReadAll(io.LimitReader(resp.Body,s.cfg.MaxSegmentBytes+1)); if err != nil { return nil,"",err }
  if int64(len(data)) > s.cfg.MaxSegmentBytes { return nil,"",fmt.Errorf("object exceeds max size") }
  ct := resp.Header.Get("Content-Type"); if ct == "" { ct="application/octet-stream" }
  return data,ct,nil
}

func (s *Server) rewriteTVPlaylist(scope string, base *url.URL, data []byte) []byte {
  lines:=strings.Split(string(data),"\n")
  for i,raw:=range lines {
    line:=strings.TrimSpace(raw); if line=="" || strings.HasPrefix(line,"#EXTM3U") { continue }
    lines[i]=tvRewriteAttributes(scope,base,raw,s)
    if !strings.HasPrefix(line,"#") {
      if resolved,err:=base.Parse(line); err==nil && resolved.Scheme=="https" && !isPrivateHost(resolved.Hostname()) { lines[i]=s.tvProxyURL(scope,resolved) }
    }
  }
  return []byte(strings.Join(lines,"\n"))
}

func tvRewriteAttributes(scope string,base *url.URL,line string,s *Server) string {
  return tvURIAttribute.ReplaceAllStringFunc(line,func(match string) string {
    m:=tvURIAttribute.FindStringSubmatch(match); if len(m)!=2 { return match }
    resolved,err:=base.Parse(m[1]); if err!=nil || resolved.Scheme!="https" || isPrivateHost(resolved.Hostname()) { return match }
    return `URI="`+s.tvProxyURL(scope,resolved)+`"`
  })
}

func (s *Server) tvProxyURL(scope string,target *url.URL) string {
  exp:=time.Now().Add(10*time.Minute).Unix(); src:=target.String(); token:=s.signTVToken(scope,src,exp)
  name:=path.Base(target.Path); if name=="." || name=="/" || name=="" { name="media" }
  return "/v1/tv/"+url.PathEscape(scope)+"/"+url.PathEscape(name)+"?src="+url.QueryEscape(src)+"&token="+url.QueryEscape(token)
}

func (s *Server) signTVToken(scope,src string,exp int64) string {
  payload:=fmt.Sprintf("%d|%s|%s",exp,scope,src); mac:=hmac.New(sha256.New,[]byte(s.cfg.PlaybackSecret)); _,_=mac.Write([]byte(payload))
  return fmt.Sprintf("%d.%s.%s",exp,scope,hex.EncodeToString(mac.Sum(nil)))
}

func (s *Server) authorizedTV(scope,src,token string) bool {
  if s.cfg.PlaybackSecret=="" { return false }
  parts:=strings.Split(token,"."); if len(parts)!=3 || parts[1]!=scope { return false }
  exp,err:=parsePositiveInt64(parts[0]); if err!=nil || exp<time.Now().Unix() { return false }
  return hmac.Equal([]byte(s.signTVToken(scope,src,exp)),[]byte(token))
}

func isHLSContent(target *url.URL,contentType string,data []byte) bool {
  t:=strings.ToLower(contentType)
  return strings.Contains(t,"mpegurl") || strings.HasSuffix(strings.ToLower(target.Path),".m3u8") || strings.HasPrefix(strings.TrimSpace(string(data)),"#EXTM3U")
}

func isPrivateHost(host string) bool {
  h:=strings.Trim(strings.ToLower(host),"[]")
  if h=="localhost" || h=="metadata.google.internal" || strings.HasSuffix(h,".local") || strings.HasSuffix(h,".internal") { return true }
  if ip:=net.ParseIP(h); ip!=nil { return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() }
  return false
}

func tvDialContext(ctx context.Context,network,address string)(net.Conn,error) {
  host,port,err:=net.SplitHostPort(address); if err!=nil { return nil,err }
  if isPrivateHost(host) { return nil,fmt.Errorf("private TV origin address") }
  ips,err:=net.DefaultResolver.LookupIPAddr(ctx,host); if err!=nil { return nil,err }
  for _,ip:=range ips { if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() { return nil,fmt.Errorf("TV origin resolved to private address") } }
  d:=net.Dialer{Timeout:5*time.Second,KeepAlive:30*time.Second}
  return d.DialContext(ctx,network,net.JoinHostPort(ips[0].IP.String(),port))
}

func hashString(value string) string { h:=sha256.Sum256([]byte(value)); return base64.RawURLEncoding.EncodeToString(h[:12]) }

func parsePositiveInt64(value string)(int64,error) {
  if value=="" { return 0,fmt.Errorf("invalid integer") }; var n int64
  for _,r:=range value { if r<'0'||r>'9' { return 0,fmt.Errorf("invalid integer") }; n=n*10+int64(r-'0'); if n<0{return 0,fmt.Errorf("integer overflow")} }
  if n<=0{return 0,fmt.Errorf("invalid integer")}; return n,nil
}

func cloneURLWithQuery(base *url.URL,q url.Values)*url.URL {
  u:=*base; merged:=u.Query()
  for k,values:=range q { for _,v:=range values { merged.Add(k,v) } }
  u.RawQuery=merged.Encode(); return &u
}

func tvDeliveryDirectives(q url.Values,playlist bool) url.Values {
  if !playlist { return nil }
  out:=url.Values{}
  for _,key:=range []string{"_HLS_msn","_HLS_part","_HLS_skip","_HLS_push","_HLS_report"} {
    for _,v:=range q[key] { if len(v)<=64 { out.Add(key,v) } }
  }
  if out.Get("_HLS_part")!="" && out.Get("_HLS_msn")=="" { out.Del("_HLS_part") }
  return out
}

func tvCacheControl(rel string,blocking bool)string {
  if strings.HasSuffix(strings.ToLower(rel),".m3u8") {
    if blocking { return "public, max-age=0, s-maxage=1, stale-while-revalidate=1, stale-if-error=6" }
    return "public, max-age=0, s-maxage=1, stale-while-revalidate=2, stale-if-error=10"
  }
  return cacheControl(rel)
}

func isPlaylistResponse(contentType string,data []byte)bool {
  return strings.Contains(strings.ToLower(contentType),"mpegurl") || strings.HasPrefix(strings.TrimSpace(string(data)),"#EXTM3U")
}

func acceptsGzip(r *http.Request)bool {
  for _,part:=range strings.Split(r.Header.Get("Accept-Encoding"),",") {
    if strings.EqualFold(strings.TrimSpace(strings.SplitN(part,";",2)[0]),"gzip") { return true }
  }
  return false
}
