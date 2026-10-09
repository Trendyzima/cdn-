package edge

import (
	"net/http"
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
  if got := tvCacheControl("tv/channel/segment.ts", false); !strings.Contains(got, "stale-if-error=300") {
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

func TestTVPrefetchParsesThirtySecondsAndExtensionlessSegments(t *testing.T) {
	base, _ := url.Parse("https://origin.example/live/index.m3u8")
	playlist := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nseg001\n#EXTINF:6.0,\nseg002.ts\n#EXTINF:6.0,\nseg003\n#EXTINF:6.0,\nseg004.m4s\n#EXTINF:6.0,\nseg005\n")
	candidates := parseTVPrefetchCandidates("channel-1", base, playlist, 30*time.Second)
	if len(candidates) != 5 { t.Fatalf("expected 5 advertised segments for a 30s window, got %d", len(candidates)) }
	var total time.Duration
	for _, candidate := range candidates { total += candidate.duration }
	if total < 30*time.Second { t.Fatalf("expected at least 30s of advertised media, got %s", total) }
	if candidates[0].name != "seg001" || candidates[2].name != "seg003" || candidates[4].name != "seg005" {
		t.Fatalf("extensionless HLS media URI was not accepted: %#v", candidates)
	}
}

func TestTVPrefetchDefaults(t *testing.T) {
  cfg := config.Load()
  if cfg.TVPrefetchSeconds < 30*time.Second { t.Fatalf("TV prefetch must be at least 30s: %s", cfg.TVPrefetchSeconds) }
  if cfg.TVPrefetchConcurrency < 1 || cfg.TVPrefetchConcurrency > 4 { t.Fatalf("TV prefetch concurrency must remain bounded: %d", cfg.TVPrefetchConcurrency) }
}
