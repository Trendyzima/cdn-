package edge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var tvURIAttribute = regexp.MustCompile(`URI="([^"]+)"`)

func (s *Server) tvAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, ok := cleanAssetPath(strings.TrimPrefix(r.URL.Path, "/v1/"))
	if !ok {
		s.rejected.Add(1)
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != "tv" {
		http.Error(w, "invalid tv path", http.StatusBadRequest)
		return
	}
	scope := parts[1]
	src := strings.TrimSpace(r.URL.Query().Get("src"))
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	target, err := url.Parse(src)
	if err != nil || target.Scheme != "https" || isPrivateHost(target.Hostname()) {
		http.Error(w, "invalid stream source", http.StatusBadRequest)
		return
	}
	if !s.authorizedTV(scope, src, token) {
		s.rejected.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	cacheKey := "tv/" + scope + "/" + hashString(src) + "/" + strings.Join(parts[2:], "/")
	if e, err := s.cache.Get(cacheKey); err == nil {
		s.hits.Add(1)
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Cache-Status", "testagram; hit")
		setType(w, rel)
		w.Header().Set("Cache-Control", cacheControl(rel))
		w.Header().Set("ETag", etagFile(e))
		s.serveEntry(w, r, e)
		return
	}

	stale, staleErr := s.cache.GetStale(cacheKey)
	s.misses.Add(1)
	data, contentType, fetchErr := s.fetchTVCoalesced(cacheKey, target, ttlForTV(s.cfg, rel))
	if fetchErr != nil {
		if staleErr == nil {
			s.staleHits.Add(1)
			w.Header().Set("X-Cache", "STALE")
			w.Header().Set("Warning", "110 - Response is stale")
			setType(w, rel)
			s.serveEntry(w, r, stale)
			return
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}

	if isHLSContent(target, contentType, data) {
		data = s.rewriteTVPlaylist(scope, target, data)
		contentType = "application/vnd.apple.mpegurl; charset=utf-8"
	}
	if _, e := s.cache.Put(cacheKey, data, ttl, s.cfg.StaleIfError); e != nil {
		http.Error(w, "cache write failed", http.StatusBadGateway)
		return
	}
	s.served.Add(uint64(len(data)))
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("Cache-Status", "testagram; fwd=uri-miss")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheControl(rel))
	w.Header().Set("ETag", ` + "`"" + `" + ` + ` + `"` + ` + ` + ` + `hash(data)` + ` + ` + `""" + "`" + ` + `)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func ttlForTV(cfg config.Config, rel string) time.Duration {
  if strings.HasSuffix(strings.ToLower(rel), ".m3u8") { return cfg.ManifestTTL }
  return cfg.SegmentTTL
}

func (s *Server) fetchTVCoalesced(key string, target *url.URL, ttl time.Duration) ([]byte, string, error) {
  s.mu.Lock()
  if f, ok := s.tvFetching[key]; ok {
    s.mu.Unlock()
    <-f.done
    return f.data, f.contentType, f.err
  }
  if e, err := s.cache.Get(key); err == nil {
    s.mu.Unlock()
    data, readErr := os.ReadFile(e.Path)
    return data, "application/octet-stream", readErr
  }
  f := &tvFetch{done: make(chan struct{})}
  s.tvFetching[key] = f
  s.mu.Unlock()

  s.inflight.Add(1)
  s.upstream.Add(1)
  f.data, f.contentType, f.err = s.fetchTVSource(target)
  s.inflight.Add(^uint64(0))
  if f.err == nil {
    if _, e := s.cache.Put(key, f.data, ttl, s.cfg.StaleIfError); e != nil {
      f.err = e
    }
  }
  s.mu.Lock()
  close(f.done)
  delete(s.tvFetching, key)
  s.mu.Unlock()
  return f.data, f.contentType, f.err
}

func (s *Server) fetchTVSource(target *url.URL) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "TestagramEdge/1.0")
	req.Header.Set("Accept", "application/vnd.apple.mpegurl,application/x-mpegURL,video/*,audio/*,*/*")
	resp, err := s.tvClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.MaxSegmentBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > s.cfg.MaxSegmentBytes {
		return nil, "", fmt.Errorf("object exceeds max size")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return data, contentType, nil
}

func (s *Server) rewriteTVPlaylist(scope string, base *url.URL, data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#EXTM3U") {
			continue
		}
		lines[i] = tvRewriteAttributes(scope, base, raw, s)
		if !strings.HasPrefix(line, "#") {
			if resolved, err := base.Parse(line); err == nil && resolved.Scheme == "https" && !isPrivateHost(resolved.Hostname()) {
				lines[i] = s.tvProxyURL(scope, resolved)
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func tvRewriteAttributes(scope string, base *url.URL, line string, s *Server) string {
	return tvURIAttribute.ReplaceAllStringFunc(line, func(match string) string {
		m := tvURIAttribute.FindStringSubmatch(match)
		if len(m) != 2 {
			return match
		}
		resolved, err := base.Parse(m[1])
		if err != nil || resolved.Scheme != "https" || isPrivateHost(resolved.Hostname()) {
			return match
		}
		return `URI="` + s.tvProxyURL(scope, resolved) + `"`
	})
}

func (s *Server) tvProxyURL(scope string, target *url.URL) string {
	exp := time.Now().Add(10 * time.Minute).Unix()
	src := target.String()
	token := s.signTVToken(scope, src, exp)
	name := path.Base(target.Path)
	if name == "." || name == "/" || name == "" {
		name = "media"
	}
	return "/v1/tv/" + url.PathEscape(scope) + "/" + url.PathEscape(name) +
		"?src=" + url.QueryEscape(src) + "&token=" + url.QueryEscape(token)
}

func (s *Server) signTVToken(scope, src string, exp int64) string {
	payload := fmt.Sprintf("%d|%s|%s", exp, scope, src)
	mac := hmac.New(sha256.New, []byte(s.cfg.PlaybackSecret))
	_, _ = mac.Write([]byte(payload))
	return fmt.Sprintf("%d.%s.%s", exp, scope, hex.EncodeToString(mac.Sum(nil)))
}

func (s *Server) authorizedTV(scope, src, token string) bool {
	if s.cfg.PlaybackSecret == "" {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] != scope {
		return false
	}
	exp, err := parsePositiveInt64(parts[0])
	if err != nil || exp < time.Now().Unix() {
		return false
	}
	expected := s.signTVToken(scope, src, exp)
	return hmac.Equal([]byte(expected), []byte(token))
}

func isHLSContent(target *url.URL, contentType string, data []byte) bool {
	t := strings.ToLower(contentType)
	return strings.Contains(t, "mpegurl") ||
		strings.HasSuffix(strings.ToLower(target.Path), ".m3u8") ||
		strings.HasPrefix(strings.TrimSpace(string(data)), "#EXTM3U")
}

func isPrivateHost(host string) bool {
	h := strings.Trim(strings.ToLower(host), "[]")
	if h == "localhost" || h == "metadata.google.internal" ||
		strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return false
}

func hashString(value string) string {
	h := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h[:12])
}

func parsePositiveInt64(value string) (int64, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid integer")
	}
	var n int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		n = n*10 + int64(r-'0')
		if n < 0 {
			return 0, fmt.Errorf("integer overflow")
		}
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid integer")
	}
	return n, nil
}
