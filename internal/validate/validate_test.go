package validate

import "testing"

func TestUsername(t *testing.T) {
	ok := []string{"Abc", "PlayerOne", "abc123", "A234567890123456"}
	for _, s := range ok {
		if err := Username(s); err != nil {
			t.Fatalf("%q should be valid: %v", s, err)
		}
	}
	bad := []string{"", "ab", "has space", "has_us", "has-dash", "toolongusername17", "ünicode", "a.b"}
	for _, s := range bad {
		if err := Username(s); err == nil {
			t.Fatalf("%q should be invalid", s)
		}
	}
}

func TestPassword(t *testing.T) {
	if err := Password("Abcd1234", "hero", 8); err != nil {
		t.Fatal(err)
	}
	if err := Password("short1", "hero", 8); err == nil {
		t.Fatal("too short")
	}
	if err := Password("allletters", "hero", 8); err == nil {
		t.Fatal("need a digit")
	}
	if err := Password("12345678", "hero", 8); err == nil {
		t.Fatal("need a letter")
	}
	if err := Password("Herohero1", "Herohero1", 8); err == nil {
		t.Fatal("must not match username")
	}
	if err := Password(`pass"word1`, "hero", 8); err == nil {
		t.Fatal("quote should be rejected")
	}
	if err := Password("Abcdefghijklm12xy", "hero", 8); err == nil {
		t.Fatal("over 16 chars")
	}
	if err := Password("Abcdefghijklm12x", "hero", 8); err != nil {
		t.Fatalf("16 chars should pass: %v", err)
	}
}

func TestEmail(t *testing.T) {
	if err := Email("user@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := Email("not-an-email"); err == nil {
		t.Fatal("expected error")
	}
	if err := Email("Name <a@b.com>"); err == nil {
		t.Fatal("display name should be rejected")
	}
}

func TestExpansion(t *testing.T) {
	v, err := Expansion("", 2)
	if err != nil || v != 2 {
		t.Fatalf("default: %d %v", v, err)
	}
	v, err = Expansion("1", 2)
	if err != nil || v != 1 {
		t.Fatalf("tbc: %d %v", v, err)
	}
	if _, err := Expansion("9", 2); err == nil {
		t.Fatal("bad expansion")
	}
}
