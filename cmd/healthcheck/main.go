package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultHealthcheckURL = "http://127.0.0.1:8080/readyz"

func checkURL(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		target = defaultHealthcheckURL
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ready" {
		return errors.New("CDN readiness endpoint did not return HTTP 200 ready")
	}
	return nil
}

func main() {
	if err := checkURL(os.Getenv("HEALTHCHECK_URL")); err != nil {
		os.Exit(1)
	}
}
