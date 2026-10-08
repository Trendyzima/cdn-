package edge

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Trendyzima/cdn-/internal/config"
)

func TestTVTokenBindsSource(t *testing.T) {
	cfg := testConfig(t)
	cfg.PlaybackSecret = "test-secret"
	s := New(cfg)
	src := "https://example.com/live/index.m3u8"
	token := s.signTVToken("abc", src, 4102444800)
	if !s.authorizedTV("abc", src, token) {
		t.Fatal("expected token to authorize")
	}
	if s.authorizedTV("abc", "https://evil.example/live.m3u8", token) {
		t.Fatal("token must be bound to source")
	}
	if s.authorizedTV("other", src, token) {
		t.Fatal("token must be bound to stream scope")
	}
}

func TestTVPlaylistRewrite(t *testing.T) {
	cfg := testConfig(t)
	cfg.PlaybackSecret = "test-secret"
	s := New(cfg)
	base, _ := url.Parse("https://example.com/live/master.m3u8")
	in := []byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nvideo/low.m3u8\n")
	out := string(s.rewriteTVPlaylist("channel-1", base, in))
	if strings.Contains(out, "audio.m3u8\"") || strings.Contains(out, "\nvideo/low.m3u8\n") {
		t.Fatalf("playlist still contains unrewritten child URLs: %s", out)
	}
	if !strings.Contains(out, "/v1/tv/channel-1/") {
		t.Fatalf("expected CDN child URL: %s", out)
	}
}

func TestTVRedirectRejectsPrivateTarget(t *testing.T) {
  cfg := testConfig(t)
  cfg.PlaybackSecret = "test-secret"
  s := New(cfg)
  base, _ := url.Parse("https://public.example/live.m3u8")
  req := &http.Request{URL: base}
  redirect, _ := url.Parse("http://127.0.0.1/private")
  req.URL = redirect
  if err := s.tvClient.CheckRedirect(req, nil); err == nil {
    t.Fatal("expected private/insecure TV redirect to be rejected")
  }
}


func TestTVCachePolicyProtectsLiveContinuity(t *testing.T) {
  if got := tvCacheControl("tv/channel/segment.ts", false); !strings.Contains(got, "stale-if-error=120") {
    t.Fatalf("segment cache policy must retain a recovery window: %s", got)
  }
  if got := tvCacheControl("tv/channel/index.m3u8", false); !strings.Contains(got, "max-age=0") {
    t.Fatalf("live playlist must not become stale in the browser: %s", got)
  }
}

func TestTVCacheKeySeparatesUpstreamVariants(t *testing.T) {
  a, _ := url.Parse("https://source-a.example/live/seg.ts")
  b, _ := url.Parse("https://source-b.example/live/seg.ts")
  if tvCacheKey("channel", a, "seg.ts") == tvCacheKey("channel", b, "seg.ts") {
    t.Fatal("different upstream sources must not share an IPTV cache key")
  }
}

func TestTVPrefetchParsesRealHLSPlaylistAndThirtySeconds(t *testing.T) {
	base, _ := url.Parse("https://origin.example/live/index.m3u8")
	playlist := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:100\n#EXT-X-PROGRAM-DATE-TIME:2026-10-08T09:00:00Z\n#EXTINF:6.0,\nseg001\n#EXTINF:6.0,\nseg002.ts\n#EXTINF:6.0,\nseg003\n#EXT-X-DISCONTINUITY\n#EXTINF:6.0,\nseg004.m4s\n#EXTINF:6.0,\nseg005\n")
	candidates := parseTVPrefetchCandidates("channel-1", base, playlist, 30*time.Second)
	if len(candidates) != 5 { t.Fatalf("expected 5 advertised media segments for a 30s window, got %d", len(candidates)) }
	var total time.Duration
	for _, candidate := range candidates { total += candidate.duration }
	if total < 30*time.Second { t.Fatalf("expected at least 30s of advertised media, got %s", total) }
	if candidates[0].name != "seg001" || candidates[2].name != "seg003" || candidates[4].name != "seg005" {
		t.Fatalf("extensionless HLS media URI was not accepted: %#v", candidates)
	}
}

func TestTVPrefetchDoesNotTreatVariantPlaylistAsMedia(t *testing.T) {
	base, _ := url.Parse("https://origin.example/live/master.m3u8")
	playlist := []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nvariant-one\n#EXT-X-STREAM-INF:BANDWIDTH=1600000\nvariant-two.m3u8\n")
	candidates := parseTVPrefetchCandidates("channel-1", base, playlist, 30*time.Second)
	if len(candidates) != 0 { t.Fatalf("master playlist variant URIs must not be prefetched as media: %#v", candidates) }
}


type tvRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f tvRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTVSelfContainedIPTV(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:6.0,\nseg001\n#EXTINF:6.0,\nseg002\n#EXTINF:6.0,\nseg003\n#EXTINF:6.0,\nseg004\n#EXTINF:6.0,\nseg005\n#EXTINF:6.0,\nseg006\n#EXTINF:6.0,\nseg007\n#EXTINF:6.0,\nseg008\n"))
		default:
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write([]byte("segment-bytes"))
		}
	}))
	defer origin.Close()

	cfg := testConfig(t)
	cfg.PlaybackSecret = "test-secret"
	cfg.TVPrefetchSeconds = 45 * time.Second
	cfg.TVPrefetchConcurrency = 4
	cfg.TVPrefetchTimeout = 5 * time.Second
	s := New(cfg)
	s.tvClient.Transport = tvRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		originURL, _ := url.Parse(origin.URL)
		u.Scheme, u.Host = originURL.Scheme, originURL.Host
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})

	src := "https://origin.test/live/index.m3u8"
	exp := time.Now().Add(10 * time.Minute).Unix()
	token := s.signTVToken("channel-e2e", src, exp)
	req := httptest.NewRequest(http.MethodGet, "/v1/tv/channel-e2e/index.m3u8?src="+url.QueryEscape(src)+"&token="+url.QueryEscape(token), nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK { t.Fatalf("playlist status=%d body=%s", rr.Code, rr.Body.String()) }
	body := rr.Body.String()
	if !strings.Contains(body, "#EXTM3U") || !strings.Contains(body, "#EXTINF:6.0") { t.Fatalf("invalid rewritten HLS playlist: %s", body) }
	if !strings.Contains(body, "/v1/tv/channel-e2e/") { t.Fatalf("playlist was not rewritten to canonical CDN paths: %s", body) }
	if got := s.tvPrefetchSuccesses.Load(); got < 1 { t.Fatalf("expected prefetch success, got %d", got) }
	if got := s.tvPrefetchWarmedSeconds.Load(); got < 30 { t.Fatalf("expected >=30 warmed seconds, got %d", got) }

	lines := strings.Split(body, "\n")
	var segmentURL string
	for _, line := range lines {
		if strings.HasPrefix(line, "/v1/tv/channel-e2e/") { segmentURL = line; break }
	}
	if segmentURL == "" { t.Fatal("expected rewritten segment URI") }
	segmentResp := httptest.NewRecorder()
	s.Handler().ServeHTTP(segmentResp, httptest.NewRequest(http.MethodGet, segmentURL, nil))
	if segmentResp.Code != http.StatusOK { t.Fatalf("rewritten segment status=%d body=%s", segmentResp.Code, segmentResp.Body.String()) }
	if segmentResp.Body.Len() == 0 { t.Fatal("rewritten segment returned empty body") }
}

func TestTVPrefetchDefaults(t *testing.T) {
  cfg := config.Load()
  if cfg.TVPrefetchSeconds < 30*time.Second { t.Fatalf("TV prefetch must be at least 30s: %s", cfg.TVPrefetchSeconds) }
  if cfg.TVPrefetchConcurrency < 1 || cfg.TVPrefetchConcurrency > 4 { t.Fatalf("TV prefetch concurrency must remain bounded: %d", cfg.TVPrefetchConcurrency) }
}
