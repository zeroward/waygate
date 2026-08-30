package wg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAndServerKeys(t *testing.T) {
	dir := t.TempDir()
	a, err := EnsureServerKeys(dir)
	if err != nil || a.Private == "" || a.Public == "" {
		t.Fatalf("%v %+v", err, a)
	}
	b, err := EnsureServerKeys(dir)
	if err != nil || b.Private != a.Private || b.Public != a.Public {
		t.Fatalf("stable keys %v %+v", err, b)
	}
}

func TestNextAddress(t *testing.T) {
	got, err := NextAddress(nil)
	if err != nil || got != "10.8.0.2/32" {
		t.Fatalf("%v %s", err, got)
	}
	got, err = NextAddress([]string{"10.8.0.2/32", "10.8.0.3/32"})
	if err != nil || got != "10.8.0.4/32" {
		t.Fatalf("%v %s", err, got)
	}
}

func TestNormalizeAllowedDropsDefaultRoute(t *testing.T) {
	got := NormalizeAllowed([]string{"0.0.0.0/0", "10.8.0.0/24", "1.2.3.4", "::/0"})
	for _, c := range got {
		if c == "0.0.0.0/0" || c == "::/0" {
			t.Fatalf("%v", got)
		}
	}
	if got[0] != VPNNet {
		t.Fatalf("vpn first %v", got)
	}
}

func TestClientConfAndZip(t *testing.T) {
	o := ClientOpts{
		Name:       "Laptop",
		Realm:      "Icecrown",
		PrivateKey: "clientpriv",
		Address:    "10.8.0.2/32",
		ServerPub:  "serverpub",
		Endpoint:   "ccraft.example:51820",
		AllowedIPs: []string{"10.8.0.0/24", "9.9.9.9/32", "0.0.0.0/0"},
	}
	conf := ClientConf(o)
	if !strings.Contains(conf, "[Interface]") || !strings.Contains(conf, "[Peer]") {
		t.Fatal(conf)
	}
	if !strings.Contains(conf, "Endpoint = ccraft.example:51820") {
		t.Fatal(conf)
	}
	if strings.Contains(conf, "0.0.0.0/0") {
		t.Fatal("full tunnel")
	}
	z, err := BundleZip(o)
	if err != nil || len(z) < 100 {
		t.Fatal(err)
	}
	if !strings.Contains(string(z), "set realmlist 10.8.0.1") {
		t.Fatal("realmlist missing")
	}
	if strings.Contains(string(z), "# set realmlist") {
		t.Fatal("realmlist should only use wg0 IP")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	got, err := NormalizeEndpoint("vpn.example.com", 51820)
	if err != nil || got != "vpn.example.com:51820" {
		t.Fatalf("%v %s", err, got)
	}
	got, err = NormalizeEndpoint("203.0.113.9:1234", 51820)
	if err != nil || got != "203.0.113.9:1234" {
		t.Fatalf("%v %s", err, got)
	}
	if _, err := NormalizeEndpoint("not a host", 51820); err == nil {
		t.Fatal("expected invalid")
	}
	if TunnelIP("10.8.0.1/24") != "10.8.0.1" {
		t.Fatal(TunnelIP("10.8.0.1/24"))
	}
}

func TestPeerFiles(t *testing.T) {
	dir := t.TempDir()
	if err := WritePeerFile(dir, 7, 3, "abcd", "10.8.0.5/32"); err != nil {
		t.Fatal(err)
	}
	bodies, err := ReadPeerDir(dir)
	if err != nil || len(bodies) != 1 || !strings.Contains(bodies[0], "PublicKey = abcd") {
		t.Fatalf("%v %v", err, bodies)
	}
	if err := RemovePeerFile(dir, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "peers", "7.peer")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestServerConfIncludesPeers(t *testing.T) {
	conf := ServerConf("serverpriv", 51820, []string{"[Peer]\nPublicKey = peerone\nAllowedIPs = 10.8.0.2/32\n"})
	if !strings.Contains(conf, "ListenPort = 51820") || !strings.Contains(conf, "peerone") {
		t.Fatal(conf)
	}
}
