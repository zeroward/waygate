package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New(time.Hour, 3)
	if !l.Allow("1.1.1.1") || !l.Allow("1.1.1.1") || !l.Allow("1.1.1.1") {
		t.Fatal("first three should pass")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("fourth should be blocked")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("other IP should pass")
	}
	if l.Remaining("1.1.1.1") != 0 {
		t.Fatalf("remaining=%d", l.Remaining("1.1.1.1"))
	}
}

func TestWindowExpiry(t *testing.T) {
	l := New(25*time.Millisecond, 1)
	if !l.Allow("k") {
		t.Fatal("first")
	}
	if l.Allow("k") {
		t.Fatal("blocked inside window")
	}
	time.Sleep(80 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("should allow after window")
	}
}
