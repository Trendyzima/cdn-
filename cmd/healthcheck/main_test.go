package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckURLRequiresReadyResponse(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
	}{
		{name: "ready", statusCode: http.StatusOK, body: "ready"},
		{name: "not ready status", statusCode: http.StatusServiceUnavailable, body: "ready", wantErr: true},
		{name: "wrong body", statusCode: http.StatusOK, body: "not ready", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			err := checkURL(server.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckURLRejectsRedirects(t *testing.T) {
	target := httptest.NewServer(http.RedirectHandler("https://example.invalid/readyz", http.StatusTemporaryRedirect))
	defer target.Close()
	if err := checkURL(target.URL); err == nil {
		t.Fatal("redirect must not satisfy readiness")
	}
}
