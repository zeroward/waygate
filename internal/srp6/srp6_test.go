package srp6

import (
	"encoding/hex"
	"testing"
)

func TestKnownVerifier(t *testing.T) {
	salt, err := hex.DecodeString("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if err != nil {
		t.Fatal(err)
	}
	got, err := CalculateVerifier("TESTUSER", "TestPass123", salt)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("dc00eb71084a7d9b0a07b5ebb526bfdca2e43756068e5d884589864b025c525e")
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("verifier mismatch\n got %s\nwant %s", hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

func TestRoundTrip(t *testing.T) {
	salt, ver, err := MakeRegistrationData("Arthas", "Frostmourne1")
	if err != nil {
		t.Fatal(err)
	}
	if len(salt) != 32 || len(ver) != 32 {
		t.Fatalf("lengths salt=%d ver=%d", len(salt), len(ver))
	}
	if !CheckLogin("arthas", "frostmourne1", salt, ver) {
		t.Fatal("same credentials (case-insensitive latin) should verify")
	}
	if CheckLogin("Arthas", "wrongPass1", salt, ver) {
		t.Fatal("wrong password must not verify")
	}
	if CheckLogin("Jaina", "Frostmourne1", salt, ver) {
		t.Fatal("wrong username must not verify")
	}
}

func TestUpperLatin(t *testing.T) {
	if UpperLatin("AbC12") != "ABC12" {
		t.Fatalf("got %q", UpperLatin("AbC12"))
	}
	if UpperLatin("p@ss-Word") != "P@SS-WORD" {
		t.Fatalf("got %q", UpperLatin("p@ss-Word"))
	}
}
