package wg

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/curve25519"
)

const (
	VPNNet     = "10.8.0.0/24"
	ServerAddr = "10.8.0.1/24"
	ServerIP   = "10.8.0.1"
)

type Keypair struct {
	Private string
	Public  string
}

func Generate() (Keypair, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return Keypair{}, err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return Keypair{}, err
	}
	return Keypair{
		Private: base64.StdEncoding.EncodeToString(priv[:]),
		Public:  base64.StdEncoding.EncodeToString(pub),
	}, nil
}

func parseKey(s string) ([32]byte, error) {
	var out [32]byte
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("invalid wireguard key")
	}
	copy(out[:], b)
	return out, nil
}

func EnsureServerKeys(dir string) (Keypair, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Keypair{}, err
	}
	keyPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")
	if b, err := os.ReadFile(keyPath); err == nil {
		priv := strings.TrimSpace(string(b))
		raw, err := parseKey(priv)
		if err != nil {
			return Keypair{}, fmt.Errorf("server key: %w", err)
		}
		pub, err := curve25519.X25519(raw[:], curve25519.Basepoint)
		if err != nil {
			return Keypair{}, err
		}
		pubStr := base64.StdEncoding.EncodeToString(pub)
		_ = os.WriteFile(pubPath, []byte(pubStr+"\n"), 0o644)
		relaxWGPerms(dir)
		return Keypair{Private: priv, Public: pubStr}, nil
	}
	kp, err := Generate()
	if err != nil {
		return Keypair{}, err
	}
	if err := os.WriteFile(keyPath, []byte(kp.Private+"\n"), 0o600); err != nil {
		return Keypair{}, err
	}
	if err := os.WriteFile(pubPath, []byte(kp.Public+"\n"), 0o644); err != nil {
		return Keypair{}, err
	}
	relaxWGPerms(dir)
	return kp, nil
}

func relaxWGPerms(dir string) {
	_ = os.Chmod(dir, 0o755)
	_ = os.Chown(dir, 1000, 1000)
	_ = os.Chmod(filepath.Join(dir, "peers"), 0o755)
	_ = os.Chown(filepath.Join(dir, "peers"), 1000, 1000)
	for _, n := range []string{"server.key", "server.pub", "wg0.conf"} {
		p := filepath.Join(dir, n)
		_ = os.Chown(p, 1000, 1000)
	}
}

func NextAddress(used []string) (string, error) {
	taken := map[string]bool{}
	for _, u := range used {
		taken[strings.TrimSpace(u)] = true
		if ip, _, err := net.ParseCIDR(u); err == nil {
			taken[ip.String()+"/32"] = true
		}
	}
	for i := 2; i <= 254; i++ {
		addr := fmt.Sprintf("10.8.0.%d/32", i)
		if !taken[addr] {
			return addr, nil
		}
	}
	return "", fmt.Errorf("VPN address pool is exhausted")
}

func WritePeerFile(dir string, id int64, userID uint32, pub, address string) error {
	if err := os.MkdirAll(filepath.Join(dir, "peers"), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("# waygate-peer id=%d user_id=%d\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\n",
		id, userID, pub, address)
	return os.WriteFile(peerPath(dir, id), []byte(body), 0o600)
}

func RemovePeerFile(dir string, id int64) error {
	err := os.Remove(peerPath(dir, id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func peerPath(dir string, id int64) string {
	return filepath.Join(dir, "peers", strconv.FormatInt(id, 10)+".peer")
}

type ClientOpts struct {
	Name       string
	Realm      string
	PrivateKey string
	Address    string
	ServerPub  string
	Endpoint   string
	AllowedIPs []string
	RealmIP    string
}

func (o ClientOpts) realmIP() string {
	if ip := strings.TrimSpace(o.RealmIP); ip != "" {
		return ip
	}
	return ServerIP
}

func ClientConf(o ClientOpts) string {
	ips := NormalizeAllowed(o.AllowedIPs)
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s\n\n", o.PrivateKey, o.Address)
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\nPersistentKeepalive = 25\n",
		o.ServerPub, strings.Join(ips, ", "), o.Endpoint)
	return b.String()
}

func TunnelIP(serverAddr string) string {
	serverAddr = strings.TrimSpace(serverAddr)
	if serverAddr == "" {
		return ServerIP
	}
	if ip, _, err := net.ParseCIDR(serverAddr); err == nil {
		return ip.String()
	}
	if ip := net.ParseIP(serverAddr); ip != nil {
		return ip.String()
	}
	return ServerIP
}

func NormalizeEndpoint(raw string, defaultPort int) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.Index(raw, "/"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("VPN endpoint is empty")
	}
	if defaultPort < 1 || defaultPort > 65535 {
		defaultPort = 51820
	}
	host, port := raw, defaultPort
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host = h
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid VPN port")
		}
		port = n
	} else if strings.Count(raw, ":") > 0 && net.ParseIP(raw) == nil {
		return "", fmt.Errorf("invalid VPN endpoint (use host:port or a hostname/IP)")
	}
	host = strings.TrimSpace(host)
	if ip := net.ParseIP(host); ip != nil {
		return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
	}
	if !validEndpointHost(host) {
		return "", fmt.Errorf("invalid VPN hostname")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func validEndpointHost(host string) bool {
	if host == "" || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" || len(part) > 63 {
			return false
		}
		for i, r := range part {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (r == '-' && i > 0 && i < len(part)-1)
			if !ok {
				return false
			}
		}
	}
	return true
}

func Realmlist(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = ServerIP
	}
	return "set realmlist " + host + "\n"
}

func Readme(realm, endpoint, realmIP string) string {
	if realmIP == "" {
		realmIP = ServerIP
	}
	return fmt.Sprintf(`%s VPN (WireGuard)

1. Install the official WireGuard app: https://www.wireguard.com/install/
2. Import the .conf file, or scan the QR on your Account page.
3. Activate the tunnel. This is a split tunnel: only this realm and website, not all internet.
4. For Wow.exe, copy realmlist.wtf from this zip (set realmlist %s). That is the VPN server address on wg0.

WireGuard endpoint (public): %s
`, realm, realmIP, endpoint)
}

func BundleZip(o ClientOpts) ([]byte, error) {
	conf := ClientConf(o)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := fileStem(o.Name)
	files := map[string]string{
		name + ".conf":  conf,
		"realmlist.wtf": Realmlist(o.realmIP()),
		"README.txt":    Readme(o.Realm, o.Endpoint, o.realmIP()),
	}
	for n, body := range files {
		h := &zip.FileHeader{Name: n, Method: zip.Store}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fileStem(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return "wireguard"
	}
	return s
}

func NormalizeAllowed(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if raw == "0.0.0.0/0" || raw == "::/0" {
			continue
		}
		if !strings.Contains(raw, "/") {
			if ip := net.ParseIP(raw); ip != nil {
				if ip.To4() != nil {
					raw += "/32"
				} else {
					raw += "/128"
				}
			}
		}
		if _, _, err := net.ParseCIDR(raw); err != nil {
			continue
		}
		if seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	if !seen[VPNNet] {
		out = append([]string{VPNNet}, out...)
	}
	sort.Strings(out)
	// keep VPN net first
	for i, c := range out {
		if c == VPNNet {
			if i != 0 {
				out = append([]string{VPNNet}, append(out[:i], out[i+1:]...)...)
			}
			break
		}
	}
	return out
}

func LookupHosts(hosts ...string) []string {
	var out []string
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if u, err := url.Parse(h); err == nil && u.Host != "" {
			h = u.Hostname()
		}
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		ips, err := net.LookupIP(h)
		if err != nil {
			if ip := net.ParseIP(h); ip != nil {
				out = append(out, h)
			}
			continue
		}
		for _, ip := range ips {
			out = append(out, ip.String())
		}
	}
	return out
}

func ServerConf(priv string, port int, peerBodies []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = %d\n\n", strings.TrimSpace(priv), port)
	for _, p := range peerBodies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString(p)
		if !strings.HasSuffix(p, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func ReadPeerDir(dir string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(dir, "peers"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".peer") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "peers", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
