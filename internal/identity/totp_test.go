package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestTOTPEnrollAndValidate(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := id.Store().StartTOTP(ctx, u.ID, u.Username, "Icecrown")
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := id.Store().ConfirmTOTP(ctx, u.ID, code)
	if err != nil || len(codes) != 8 {
		t.Fatalf("%v %v", err, codes)
	}
	if !id.TOTPEnabled(ctx, u.ID) {
		t.Fatal("expected enabled")
	}
	code, _ = totp.GenerateCode(secret, time.Now())
	if err := id.Store().ValidateTOTP(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}
	if err := id.Store().ValidateTOTP(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	if err := id.Store().ValidateTOTP(ctx, u.ID, codes[0]); err == nil {
		t.Fatal("recovery reuse")
	}
	if _, _, err := id.Store().StartTOTP(ctx, u.ID, u.Username, "Icecrown"); !errors.Is(err, ErrTOTPEnabled) {
		t.Fatalf("start while enabled %v", err)
	}
	if !id.TOTPEnabled(ctx, u.ID) {
		t.Fatal("start must not disable live TOTP")
	}
}

func TestQRDataURI(t *testing.T) {
	u := "otpauth://totp/Icecrown:HEROONE?secret=MFRGGZDFMY&issuer=Icecrown"
	got, err := QRDataURI(u)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("qr prefix %q", got[:min(40, len(got))])
	}
	raw, err := base64.StdEncoding.DecodeString(got[len(prefix):])
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 100 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("not a png len=%d", len(raw))
	}
	if _, err := QRDataURI(""); err == nil {
		t.Fatal("empty otpauth should fail")
	}
}
