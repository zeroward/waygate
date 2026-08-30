package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/config"
)

type Verifier struct {
	cfg  config.Config
	http *http.Client
}

func New(cfg config.Config) *Verifier {
	return &Verifier{
		cfg:  cfg,
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (v *Verifier) Required() bool {
	if v.cfg.DemoMode {
		return false
	}
	return v.cfg.CaptchaConfigured()
}

func (v *Verifier) SiteKey() string {
	switch v.cfg.CaptchaProvider {
	case "turnstile":
		return v.cfg.TurnstileSiteKey
	case "hcaptcha":
		return v.cfg.HCaptchaSiteKey
	default:
		return ""
	}
}

func (v *Verifier) Provider() string {
	if !v.Required() {
		return "none"
	}
	return v.cfg.CaptchaProvider
}

func (v *Verifier) Verify(ctx context.Context, token, ip string) error {
	if !v.Required() {
		return nil
	}
	if token == "" {
		return fmt.Errorf("complete the captcha")
	}
	switch v.cfg.CaptchaProvider {
	case "turnstile":
		return v.post(ctx, "https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
			"secret":   {v.cfg.TurnstileSecret},
			"response": {token},
			"remoteip": {ip},
		})
	case "hcaptcha":
		return v.post(ctx, "https://api.hcaptcha.com/siteverify", url.Values{
			"secret":   {v.cfg.HCaptchaSecret},
			"response": {token},
			"remoteip": {ip},
		})
	default:
		return nil
	}
}

func (v *Verifier) post(ctx context.Context, endpoint string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("captcha: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := v.http.Do(req)
	if err != nil {
		return fmt.Errorf("captcha unavailable")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<10))
	if err != nil {
		return fmt.Errorf("captcha unavailable")
	}
	var parsed struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("captcha unavailable")
	}
	if !parsed.Success {
		return fmt.Errorf("captcha verification failed")
	}
	return nil
}
