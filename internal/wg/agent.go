package wg

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Agent struct {
	Dir        string
	Iface      string
	ServerAddr string
	Port       int
	Listen     string
	AuthPort   int
	WorldPort  int
	SitePort   int
	Log        *slog.Logger
	run        func(name string, args ...string) error
}

func NewAgent(dir, iface, serverAddr, listen string, port, authPort, worldPort, sitePort int, log *slog.Logger) *Agent {
	if iface == "" {
		iface = "wg0"
	}
	if serverAddr == "" {
		serverAddr = ServerAddr
	}
	if port == 0 {
		port = 51820
	}
	if authPort == 0 {
		authPort = 3724
	}
	if worldPort == 0 {
		worldPort = 28085
	}
	if sitePort == 0 {
		sitePort = 3080
	}
	if log == nil {
		log = slog.Default()
	}
	return &Agent{
		Dir:        dir,
		Iface:      iface,
		ServerAddr: serverAddr,
		Port:       port,
		Listen:     listen,
		AuthPort:   authPort,
		WorldPort:  worldPort,
		SitePort:   sitePort,
		Log:        log,
		run: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}
}

func (a *Agent) Run(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(a.Dir, "peers"), 0o700); err != nil {
		return err
	}
	if _, err := EnsureServerKeys(a.Dir); err != nil {
		return err
	}
	if err := a.sync(); err != nil {
		a.Log.Error("wg-agent initial sync", "err", err)
	} else {
		a.Log.Info("wg-agent up", "iface", a.Iface, "addr", a.ServerAddr, "port", a.Port)
	}
	go a.serveHealth()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := a.sync(); err != nil {
				a.Log.Error("wg-agent sync", "err", err)
			}
		}
	}
}

func (a *Agent) serveHealth() {
	if strings.TrimSpace(a.Listen) == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: a.Listen, Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	ln, err := net.Listen("tcp", a.Listen)
	if err != nil {
		a.Log.Error("wg-agent health listen", "err", err, "addr", a.Listen)
		return
	}
	a.Log.Info("wg-agent health", "addr", a.Listen)
	_ = srv.Serve(ln)
}

func (a *Agent) sync() error {
	keys, err := EnsureServerKeys(a.Dir)
	if err != nil {
		return err
	}
	bodies, err := ReadPeerDir(a.Dir)
	if err != nil {
		return err
	}
	conf := ServerConf(keys.Private, a.Port, bodies)
	confPath := filepath.Join(a.Dir, "wg0.conf")
	prev, _ := os.ReadFile(confPath)
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		return err
	}
	relaxWGPerms(a.Dir)
	if err := a.ensureLink(); err != nil {
		return err
	}
	if string(prev) != conf {
		if err := a.run("wg", "syncconf", a.Iface, confPath); err != nil {
			if err2 := a.run("wg", "setconf", a.Iface, confPath); err2 != nil {
				return err2
			}
		}
	}
	if err := a.ensureAddr(); err != nil {
		return err
	}
	if err := a.run("ip", "link", "set", a.Iface, "up"); err != nil {
		return err
	}
	return a.ensureForward()
}

func (a *Agent) ensureLink() error {
	if err := a.run("ip", "link", "show", "dev", a.Iface); err == nil {
		return nil
	}
	return a.run("ip", "link", "add", "dev", a.Iface, "type", "wireguard")
}

func (a *Agent) ensureAddr() error {
	if err := a.run("ip", "addr", "show", "dev", a.Iface); err == nil {
		// add is idempotent-enough; ignore "File exists"
		if err := a.run("ip", "addr", "add", a.ServerAddr, "dev", a.Iface); err != nil && !strings.Contains(err.Error(), "File exists") {
			return err
		}
		return nil
	}
	return a.run("ip", "addr", "add", a.ServerAddr, "dev", a.Iface)
}

func (a *Agent) ensureForward() error {
	_ = a.run("sysctl", "-w", "net.ipv4.ip_forward=1")
	rules := [][]string{
		{"iptables", "-C", "FORWARD", "-i", a.Iface, "-j", "ACCEPT"},
		{"iptables", "-C", "FORWARD", "-o", a.Iface, "-j", "ACCEPT"},
	}
	adds := [][]string{
		{"iptables", "-A", "FORWARD", "-i", a.Iface, "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-o", a.Iface, "-j", "ACCEPT"},
	}
	for i, check := range rules {
		if err := a.run(check[0], check[1:]...); err != nil {
			if err := a.run(adds[i][0], adds[i][1:]...); err != nil {
				return err
			}
		}
	}
	masq := []string{"-t", "nat", "-C", "POSTROUTING", "-s", VPNNet, "!", "-d", VPNNet, "-j", "MASQUERADE"}
	if err := a.run("iptables", masq...); err != nil {
		masq[2] = "-A"
		if err := a.run("iptables", masq...); err != nil {
			return err
		}
	}
	for _, port := range []int{a.AuthPort, a.WorldPort, a.SitePort} {
		check := []string{"-t", "nat", "-C", "PREROUTING", "-d", ServerIP, "-p", "tcp", "--dport", fmt.Sprintf("%d", port), "-j", "REDIRECT", "--to-ports", fmt.Sprintf("%d", port)}
		if err := a.run("iptables", check...); err != nil {
			add := append([]string{}, check...)
			add[2] = "-A"
			if err := a.run("iptables", add...); err != nil {
				return err
			}
		}
	}
	return nil
}
