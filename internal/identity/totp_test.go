package identity

import (
	"context"
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
}
