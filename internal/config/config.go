package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	DemoMode   bool
	LogLevel   string
	TrustProxy bool
	SiteURL    string

	SessionSecure bool
	SessionTTL    time.Duration

	CoreName         string
	RealmName        string
	PublicHost       string
	PublicAuthPort   int
	PublicWorldPort  int
	DefaultExpansion uint8
	SiteBlurb        string

	MySQLHost     string
	MySQLPort     int
	MySQLUser     string
	MySQLPassword string
	AuthDB        string
	CharactersDB  string
	WorldDB       string

	WorldHost string
	WorldPort int
	AuthHost  string
	AuthPort  int

	SOAPEnabled  bool
	SOAPHost     string
	SOAPPort     int
	SOAPUsername string
	SOAPPassword string
	SOAPURI      string
	SOAPTimeout  time.Duration
	AccountMode  string // auto | soap | sql

	BotPrefixes        []string
	HideGM             bool
	GMMinLevel         uint8
	StatusCache        time.Duration
	LeaderboardSize    int
	RequireUniqueEmail bool
	PasswordMinLength  int

	CaptchaProvider  string
	TurnstileSiteKey string
	TurnstileSecret  string
	HCaptchaSiteKey  string
	HCaptchaSecret   string

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      bool
	ContactEmail string
	DiscordURL   string

	RateWindow   time.Duration
	RateRegister int
	RateLogin    int
	RateContact  int
	RateReset    int
	RateKB       int
	RateUnstuck  int
	RateTickets  int

	WowCredentialsMax int

	HowToConnectFile string
	KBPath           string
	DownloadsDir     string
	DownloadsCatalog string
	DownloadsMaxMB   int
	ClamAVAddr       string
	ClamAVTimeout    time.Duration
	ClamAVScanMaxMB  int
	ModulesDir       string
}

func Load() (Config, error) {
	_ = LoadDotEnv(".env")

	c := Config{
		ListenAddr:         env("LISTEN_ADDR", ":3080"),
		DemoMode:           envBool("DEMO_MODE", false),
		LogLevel:           strings.ToLower(env("LOG_LEVEL", "info")),
		TrustProxy:         envBool("TRUST_PROXY", false),
		SiteURL:            strings.TrimRight(env("SITE_URL", "http://127.0.0.1:3080"), "/"),
		SessionSecure:      envBool("SESSION_SECURE_COOKIE", false),
		SessionTTL:         time.Duration(envInt("SESSION_TTL_HOURS", 24)) * time.Hour,
		CoreName:           env("CORE_NAME", "AzerothCore WotLK 3.3.5a"),
		RealmName:          env("REALM_NAME", "Icecrown"),
		PublicHost:         env("PUBLIC_HOST", "127.0.0.1"),
		PublicAuthPort:     envInt("PUBLIC_AUTH_PORT", 3724),
		PublicWorldPort:    envInt("PUBLIC_WORLD_PORT", 28085),
		DefaultExpansion:   uint8(envInt("DEFAULT_EXPANSION", 2)),
		SiteBlurb:          env("SITE_BLURB", "Wrath of the Lich King 3.3.5a. Real players only on the status boards."),
		MySQLHost:          env("MYSQL_HOST", "ac-database"),
		MySQLPort:          envInt("MYSQL_PORT", 3306),
		MySQLUser:          env("MYSQL_USER", "webreg"),
		MySQLPassword:      env("MYSQL_PASSWORD", ""),
		AuthDB:             env("AUTH_DB", "acore_auth"),
		CharactersDB:       env("CHARACTERS_DB", "acore_characters"),
		WorldDB:            env("WORLD_DB", "acore_world"),
		WorldHost:          env("WORLD_HOST", "ac-worldserver"),
		WorldPort:          envInt("WORLD_PORT", 8085),
		AuthHost:           env("AUTH_HOST", "ac-authserver"),
		AuthPort:           envInt("AUTH_PORT", 3724),
		SOAPEnabled:        envBool("SOAP_ENABLED", true),
		SOAPHost:           env("SOAP_HOST", "ac-worldserver"),
		SOAPPort:           envInt("SOAP_PORT", 7878),
		SOAPUsername:       env("SOAP_USERNAME", ""),
		SOAPPassword:       env("SOAP_PASSWORD", ""),
		SOAPURI:            env("SOAP_URI", "urn:AC"),
		SOAPTimeout:        time.Duration(envInt("SOAP_TIMEOUT_SECONDS", 8)) * time.Second,
		AccountMode:        strings.ToLower(env("ACCOUNT_CREATE_MODE", "auto")),
		HideGM:             envBool("HIDE_GM", false),
		GMMinLevel:         uint8(envInt("GM_MIN_LEVEL", 1)),
		StatusCache:        time.Duration(envInt("STATUS_CACHE_SECONDS", 20)) * time.Second,
		LeaderboardSize:    envInt("LEADERBOARD_SIZE", 20),
		RequireUniqueEmail: envBool("REQUIRE_UNIQUE_EMAIL", true),
		PasswordMinLength:  envInt("PASSWORD_MIN_LENGTH", 8),
		CaptchaProvider:    strings.ToLower(env("CAPTCHA_PROVIDER", "none")),
		TurnstileSiteKey:   env("TURNSTILE_SITE_KEY", ""),
		TurnstileSecret:    env("TURNSTILE_SECRET_KEY", ""),
		HCaptchaSiteKey:    env("HCAPTCHA_SITE_KEY", ""),
		HCaptchaSecret:     env("HCAPTCHA_SECRET", ""),
		SMTPHost:           env("SMTP_HOST", ""),
		SMTPPort:           envInt("SMTP_PORT", 587),
		SMTPUser:           env("SMTP_USER", ""),
		SMTPPassword:       env("SMTP_PASSWORD", ""),
		SMTPFrom:           env("SMTP_FROM", ""),
		SMTPTLS:            envBool("SMTP_TLS", true),
		ContactEmail:       env("CONTACT_EMAIL", ""),
		DiscordURL:         env("DISCORD_URL", ""),
		RateWindow:         time.Duration(envInt("RATE_LIMIT_WINDOW_MINUTES", 15)) * time.Minute,
		RateRegister:       envInt("RATE_LIMIT_REGISTER", 5),
		RateLogin:          envInt("RATE_LIMIT_LOGIN", 10),
		RateContact:        envInt("RATE_LIMIT_CONTACT", 3),
		RateReset:          envInt("RATE_LIMIT_RESET", 3),
		RateKB:             envInt("RATE_LIMIT_KB", 20),
		RateUnstuck:        envInt("RATE_LIMIT_UNSTUCK", 5),
		RateTickets:        envInt("RATE_LIMIT_TICKETS", 5),
		WowCredentialsMax:  envInt("WOW_CREDENTIALS_MAX", 5),
		HowToConnectFile:   env("HOW_TO_CONNECT_FILE", "content/how-to-connect.md"),
		KBPath:             env("KB_PATH", "data/kb.sqlite"),
		DownloadsDir:       env("DOWNLOADS_DIR", "downloads"),
		DownloadsCatalog:   env("DOWNLOADS_CATALOG", ""),
		DownloadsMaxMB:     envInt("DOWNLOADS_MAX_UPLOAD_MB", 20480),
		ClamAVAddr:         env("CLAMAV_ADDR", ""),
		ClamAVTimeout:      time.Duration(envInt("CLAMAV_TIMEOUT_SECONDS", 600)) * time.Second,
		ClamAVScanMaxMB:    envInt("CLAMAV_SCAN_MAX_MB", 100),
		ModulesDir:         env("MODULES_DIR", ""),
	}

	c.BotPrefixes = parsePrefixes(env("BOT_USERNAME_PREFIXES", "rndbot"))

	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if !identOK(c.AuthDB) || !identOK(c.CharactersDB) {
		return fmt.Errorf("database names must be alphanumeric/underscore")
	}
	if c.WorldDB != "" && !identOK(c.WorldDB) {
		return fmt.Errorf("WORLD_DB must be alphanumeric/underscore")
	}
	switch c.AccountMode {
	case "auto", "soap", "sql":
	default:
		return fmt.Errorf("ACCOUNT_CREATE_MODE must be auto, soap, or sql")
	}
	switch c.CaptchaProvider {
	case "none", "turnstile", "hcaptcha":
	default:
		return fmt.Errorf("CAPTCHA_PROVIDER must be none, turnstile, or hcaptcha")
	}
	if c.DefaultExpansion > 2 {
		return fmt.Errorf("DEFAULT_EXPANSION must be 0, 1, or 2")
	}
	if c.PasswordMinLength < 8 {
		return fmt.Errorf("PASSWORD_MIN_LENGTH must be >= 8")
	}
	if c.StatusCache < 5*time.Second {
		c.StatusCache = 5 * time.Second
	}
	if c.LeaderboardSize < 1 || c.LeaderboardSize > 100 {
		return fmt.Errorf("LEADERBOARD_SIZE must be 1–100")
	}
	if c.GMMinLevel == 0 {
		c.GMMinLevel = 1
	}
	if c.GMMinLevel > 4 {
		return fmt.Errorf("GM_MIN_LEVEL must be 1–4")
	}
	if c.RateKB < 1 {
		c.RateKB = 20
	}
	if c.RateUnstuck < 1 {
		c.RateUnstuck = 5
	}
	if c.RateTickets < 1 {
		c.RateTickets = 5
	}
	if c.WowCredentialsMax < 1 {
		c.WowCredentialsMax = 5
	}
	if c.WowCredentialsMax > 20 {
		c.WowCredentialsMax = 20
	}
	return nil
}

func (c Config) SMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPFrom != ""
}

func (c Config) CaptchaConfigured() bool {
	switch c.CaptchaProvider {
	case "turnstile":
		return c.TurnstileSiteKey != "" && c.TurnstileSecret != ""
	case "hcaptcha":
		return c.HCaptchaSiteKey != "" && c.HCaptchaSecret != ""
	default:
		return false
	}
}

func (c Config) SOAPConfigured() bool {
	return c.SOAPEnabled && c.SOAPHost != "" && c.SOAPUsername != "" && c.SOAPPassword != ""
}

func (c Config) ClamAVScanMaxBytes() int64 {
	mb := c.ClamAVScanMaxMB
	if mb < 1 {
		mb = 100
	}
	return int64(mb) * 1024 * 1024
}

func (c Config) DownloadsMaxBytes() int64 {
	mb := c.DownloadsMaxMB
	if mb < 1 {
		mb = 20480
	}
	if mb > 65536 {
		mb = 65536
	}
	return int64(mb) * 1024 * 1024
}

func identOK(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func parsePrefixes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, strings.ToUpper(p))
	}
	if len(out) == 0 {
		out = []string{"RNDBOT"}
	}
	return out
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}
