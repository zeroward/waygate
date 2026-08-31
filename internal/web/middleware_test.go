package web

import (
	"strings"
	"testing"

	"github.com/zeroward/waygate/internal/captcha"
	"github.com/zeroward/waygate/internal/config"
)

func TestCSPHCaptchaConnect(t *testing.T) {
	cfg := config.Config{
		CaptchaProvider: "hcaptcha",
		HCaptchaSiteKey: "site",
		HCaptchaSecret:  "secret",
	}
	s := &Server{cfg: cfg, captcha: captcha.New(cfg)}
	got := s.csp()
	if !strings.Contains(got, "connect-src 'self' https://hcaptcha.com https://*.hcaptcha.com https://*.w.hcaptcha.com") {
		t.Fatalf("connect-src %s", got)
	}
	if !strings.Contains(got, "script-src 'self' https://hcaptcha.com") {
		t.Fatalf("script-src %s", got)
	}
	if !strings.Contains(got, "img-src 'self' data: blob: https://wow.zamimg.com https://hcaptcha.com") {
		t.Fatalf("img-src %s", got)
	}
	if strings.Contains(got, "script-src 'self' https://wow.zamimg.com") {
		t.Fatal("zamimg must not be in script-src")
	}
}

func TestCSPNoneConnectSelf(t *testing.T) {
	cfg := config.Config{CaptchaProvider: "none"}
	s := &Server{cfg: cfg, captcha: captcha.New(cfg)}
	got := s.csp()
	if !strings.Contains(got, "connect-src 'self'") {
		t.Fatalf("connect %s", got)
	}
	if strings.Contains(got, "hcaptcha.com") {
		t.Fatalf("hcaptcha leaked %s", got)
	}
}
