// Package soap talks to AzerothCore worldserver SOAP (executeCommand).
// Credentials stay server-side. Commands that include passwords are never logged.
package soap

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxBody = 64 << 10

type Client struct {
	BaseURL  string
	Username string
	Password string
	URI      string
	HTTP     *http.Client
}

func New(host string, port int, user, pass, uri string, timeout time.Duration) *Client {
	if uri == "" {
		uri = "urn:AC"
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Client{
		BaseURL:  fmt.Sprintf("http://%s:%d/", host, port),
		Username: user,
		Password: pass,
		URI:      uri,
		HTTP:     &http.Client{Timeout: timeout},
	}
}

func (c *Client) CreateAccount(ctx context.Context, username, password, email string) error {
	if err := rejectUnsafe(username, password, email); err != nil {
		return err
	}
	cmd := BuildCreateCommand(username, password, email)
	_, err := c.Execute(ctx, cmd)
	if err != nil && email != "" && looksLikeArgError(err) {
		_, err = c.Execute(ctx, BuildCreateCommand(username, password, ""))
	}
	return wrapAccountErr(err)
}

func (c *Client) SetPassword(ctx context.Context, username, password string) error {
	if err := rejectUnsafe(username, password); err != nil {
		return err
	}
	_, err := c.Execute(ctx, BuildSetPasswordCommand(username, password))
	return wrapAccountErr(err)
}

func (c *Client) SetAddon(ctx context.Context, username string, expansion uint8) error {
	if err := rejectUnsafe(username); err != nil {
		return err
	}
	_, err := c.Execute(ctx, fmt.Sprintf("account set addon %s %d", quote(username), expansion))
	return err
}

func (c *Client) SetGMLevel(ctx context.Context, username string, level uint8) error {
	if err := rejectUnsafe(username); err != nil {
		return err
	}
	if level > RankSuperGMCap {
		return fmt.Errorf("invalid gm level")
	}
	_, err := c.Execute(ctx, BuildSetGMLevelCommand(username, level))
	return err
}

const RankSuperGMCap = 4

func BuildSetGMLevelCommand(username string, level uint8) string {
	return fmt.Sprintf("account set gmlevel %s %d -1", quote(username), level)
}

func (c *Client) SetEmail(ctx context.Context, username, email string) error {
	if err := rejectUnsafe(username, email); err != nil {
		return err
	}
	_, err := c.Execute(ctx, fmt.Sprintf("account set email %s %s %s", quote(username), quote(email), quote(email)))
	return err
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Execute(ctx, "server info")
	return err
}

func (c *Client) BanAccount(ctx context.Context, username, duration, reason string) error {
	if err := rejectUnsafe(username, duration, reason); err != nil {
		return err
	}
	_, err := c.Execute(ctx, BuildBanAccountCommand(username, duration, reason))
	return err
}

func (c *Client) UnbanAccount(ctx context.Context, username string) error {
	if err := rejectUnsafe(username); err != nil {
		return err
	}
	_, err := c.Execute(ctx, BuildUnbanAccountCommand(username))
	return err
}

func BuildBanAccountCommand(username, duration, reason string) string {
	return "ban account " + quote(username) + " " + duration + " " + quote(reason)
}

func BuildUnbanAccountCommand(username string) string {
	return "unban account " + quote(username)
}

func (c *Client) Unstuck(ctx context.Context, character string) error {
	if err := rejectUnsafe(character); err != nil {
		return err
	}
	_, err := c.Execute(ctx, BuildUnstuckCommand(character))
	return err
}

func BuildUnstuckCommand(character string) string {
	return "unstuck " + quote(character) + " inn"
}

func (c *Client) Execute(ctx context.Context, command string) (string, error) {
	body := Envelope(command)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.SetBasicAuth(c.Username, c.Password)

	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("soap unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("soap read: %w", err)
	}
	if res.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("soap unauthorized")
	}
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("soap http %d", res.StatusCode)
	}
	result, fault, err := ParseResponse(raw)
	if err != nil {
		return "", err
	}
	if fault != "" {
		return "", fmt.Errorf("soap: %s", fault)
	}
	return result, nil
}

func Envelope(command string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns1="urn:AC">`)
	b.WriteString(`<SOAP-ENV:Body><ns1:executeCommand><command>`)
	_ = xml.EscapeText(&b, []byte(command))
	b.WriteString(`</command></ns1:executeCommand></SOAP-ENV:Body></SOAP-ENV:Envelope>`)
	return b.String()
}

func BuildCreateCommand(username, password, email string) string {
	if email == "" {
		return "account create " + quote(username) + " " + quote(password)
	}
	return "account create " + quote(username) + " " + quote(password) + " " + quote(email)
}

func BuildSetPasswordCommand(username, password string) string {
	q := quote(password)
	return "account set password " + quote(username) + " " + q + " " + q
}

func quote(s string) string {
	return `"` + s + `"`
}

func rejectUnsafe(vals ...string) error {
	for _, s := range vals {
		if strings.ContainsAny(s, "\"\\\x00\r\n") {
			return fmt.Errorf("value contains characters that cannot be sent via SOAP")
		}
		for _, r := range s {
			if r < 32 {
				return fmt.Errorf("value contains control characters")
			}
		}
	}
	return nil
}

func looksLikeArgError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "syntax") || strings.Contains(s, "incorrect") || strings.Contains(s, "expected")
}

func wrapAccountErr(err error) error {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "already") || strings.Contains(s, "exist"):
		return fmt.Errorf("username is already taken")
	case strings.Contains(s, "unauthorized"):
		return fmt.Errorf("account service misconfigured")
	default:
		return fmt.Errorf("account service error")
	}
}

type soapEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    soapBody `xml:"Body"`
}

type soapBody struct {
	Fault    *soapFault `xml:"Fault"`
	Response *struct {
		Result string `xml:"result"`
	} `xml:"executeCommandResponse"`
}

type soapFault struct {
	FaultString string `xml:"faultstring"`
	FaultCode   string `xml:"faultcode"`
}

func ParseResponse(raw []byte) (result, fault string, err error) {
	var env soapEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return "", "", fmt.Errorf("soap xml: %w", err)
	}
	if env.Body.Fault != nil && env.Body.Fault.FaultString != "" {
		return "", env.Body.Fault.FaultString, nil
	}
	if env.Body.Response != nil {
		return env.Body.Response.Result, "", nil
	}
	return "", "", fmt.Errorf("soap: empty response")
}
