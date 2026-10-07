package edge

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
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

func TestTVBlockingReloadDirectives(t *testing.T) {
 q:=url.Values{"_HLS_msn":[]string{"42"},"_HLS_part":[]string{"3"},"_HLS_skip":[]string{"YES"}}
 d:=tvDeliveryDirectives(q,true)
 if d.Get("_HLS_msn")!="42" || d.Get("_HLS_part")!="3" || d.Get("_HLS_skip")!="YES" { t.Fatalf("directives not preserved: %v",d) }
 d=tvDeliveryDirectives(url.Values{"_HLS_part":[]string{"3"}},true)
 if d.Get("_HLS_part")!="" { t.Fatal("part directive must not be sent without msn") }
}
