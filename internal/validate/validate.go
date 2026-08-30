package validate

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
)

const (
	UsernameMin     = 3
	UsernameMax     = 16
	PasswordMax     = 16 // WoW 3.3.5a client limit
	SitePasswordMax = 128
	EmailMax        = 255
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9]{3,16}$`)

func Username(s string) error {
	s = strings.TrimSpace(s)
	if !usernameRe.MatchString(s) {
		return fmt.Errorf("username must be %d–%d letters or digits", UsernameMin, UsernameMax)
	}
	return nil
}

func Password(s, username string, minLen int) error {
	if minLen < 8 {
		minLen = 8
	}
	if len(s) < minLen || len(s) > PasswordMax {
		return fmt.Errorf("password must be %d–%d characters", minLen, PasswordMax)
	}
	if strings.ContainsAny(s, "\"\\\x00\r\n") {
		return fmt.Errorf("password contains disallowed characters")
	}
	for _, r := range s {
		if r < 32 || r > 126 {
			return fmt.Errorf("password must be printable ASCII")
		}
	}
	var letter, digit bool
	for _, r := range s {
		if unicode.IsLetter(r) {
			letter = true
		}
		if unicode.IsDigit(r) {
			digit = true
		}
	}
	if !letter || !digit {
		return fmt.Errorf("password must include at least one letter and one number")
	}
	if username != "" && strings.EqualFold(s, username) {
		return fmt.Errorf("password must not match username")
	}
	return nil
}

func SitePassword(s, username string, minLen int) error {
	if minLen < 8 {
		minLen = 8
	}
	if len(s) < minLen || len(s) > SitePasswordMax {
		return fmt.Errorf("password must be %d–%d characters", minLen, SitePasswordMax)
	}
	if strings.ContainsAny(s, "\"\\\x00\r\n") {
		return fmt.Errorf("password contains disallowed characters")
	}
	var letter, digit bool
	for _, r := range s {
		if r < 32 || r > 126 {
			return fmt.Errorf("password must be printable ASCII")
		}
		if unicode.IsLetter(r) {
			letter = true
		}
		if unicode.IsDigit(r) {
			digit = true
		}
	}
	if !letter || !digit {
		return fmt.Errorf("password must include at least one letter and one number")
	}
	if username != "" && strings.EqualFold(s, username) {
		return fmt.Errorf("password must not match username")
	}
	return nil
}

func Email(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("email is required")
	}
	if len(s) > EmailMax {
		return fmt.Errorf("email is too long")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return fmt.Errorf("email is not valid")
	}
	if !strings.Contains(s, ".") {
		return fmt.Errorf("email is not valid")
	}
	return nil
}

func Expansion(raw string, def uint8) (uint8, error) {
	if strings.TrimSpace(raw) == "" {
		if def > 2 {
			def = 2
		}
		return def, nil
	}
	switch strings.TrimSpace(raw) {
	case "0":
		return 0, nil
	case "1":
		return 1, nil
	case "2":
		return 2, nil
	default:
		return 0, fmt.Errorf("expansion must be 0 (Classic), 1 (TBC), or 2 (WotLK)")
	}
}

func SOAPSafe(s string) error {
	if strings.ContainsAny(s, "\"\\\x00\r\n") {
		return fmt.Errorf("value contains disallowed characters")
	}
	return nil
}
