package identity

import (
	"context"
	"strconv"
	"testing"
)

func TestWGPeerCapAndOwner(t *testing.T) {
	id, _ := testID(t)
	ctx := context.Background()
	u, err := id.Register(ctx, "HeroOne", "Abcd1234", "h@example.com", "", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := id.Register(ctx, "HeroTwo", "Abcd1234", "t@example.com", "HeroTwo", "Abcd1234", 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err := id.Store().InsertWGPeer(ctx, u.ID, "dev", "pub"+string(rune('A'+i)), "priv", "10.8.0."+strconv.Itoa(i+2)+"/32", 5)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := id.Store().InsertWGPeer(ctx, u.ID, "dev", "pubZ", "priv", "10.8.0.20/32", 5); err != ErrTooManyWG {
		t.Fatalf("cap %v", err)
	}
	list, err := id.Store().ListWGPeers(ctx, u.ID)
	if err != nil || len(list) != 5 {
		t.Fatalf("list %v %d", err, len(list))
	}
	if _, err := id.Store().GetWGPeer(ctx, u2.ID, list[0].ID); err != ErrNotFound {
		t.Fatalf("cross %v", err)
	}
	if err := id.Store().DeleteWGPeer(ctx, u.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := id.Store().DeleteWGPeer(ctx, u.ID, list[0].ID); err != ErrNotFound {
		t.Fatalf("gone %v", err)
	}
}
