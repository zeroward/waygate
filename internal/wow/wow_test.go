package wow

import "testing"

func TestGold(t *testing.T) {
	cases := map[uint32]string{
		0:     "0c",
		50:    "50c",
		100:   "1s",
		150:   "1s 50c",
		10000: "1g",
		10005: "1g 5c",
		10100: "1g 1s",
		12345: "1g 23s 45c",
	}
	for in, want := range cases {
		if got := Gold(in); got != want {
			t.Errorf("Gold(%d)=%q want %q", in, got, want)
		}
	}
}

func TestLocation(t *testing.T) {
	if got := Location(571, 210); got != "Icecrown · Northrend" {
		t.Fatalf("icecrown %q", got)
	}
	if got := Location(0, 1519); got != "Stormwind City · Eastern Kingdoms" {
		t.Fatalf("stormwind %q", got)
	}
}

func TestLogoutLabel(t *testing.T) {
	if LogoutLabel(true, 0) != "Online" {
		t.Fatal("online")
	}
	if LogoutLabel(false, 0) != "—" {
		t.Fatal("never")
	}
	if got := LogoutLabel(false, 1); got == "Online" || got == "—" {
		t.Fatalf("unix 1: %q", got)
	}
}
