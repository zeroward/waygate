package web

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/wg"
)

type wgPeerView struct {
	identity.WGPeer
	QR template.URL
}

func (s *Server) wgOn() bool {
	return s.cfg.WGEnabled && s.wgOK
}

func (s *Server) wgAllowedIPs() []string {
	hosts := wg.LookupHosts(s.cfg.PublicHost, s.cfg.SiteURL)
	return wg.NormalizeAllowed(append(append([]string{wg.VPNNet}, hosts...), s.cfg.WGExtraNets...))
}

func (s *Server) wgEndpoint() string {
	raw := ""
	if s.id != nil {
		raw = s.id.Store().WGEndpoint()
	}
	if raw == "" {
		raw = strings.TrimSpace(s.cfg.WGEndpoint)
	}
	if raw == "" {
		raw = strings.TrimSpace(s.cfg.PublicHost)
	}
	ep, err := wg.NormalizeEndpoint(raw, s.cfg.WGPort)
	if err != nil {
		return s.cfg.WGEndpointHost()
	}
	return ep
}

func (s *Server) wgClientOpts(p identity.WGPeer, serverPub string) wg.ClientOpts {
	return wg.ClientOpts{
		Name:       p.Name,
		Realm:      s.cfg.RealmName,
		PrivateKey: p.PrivateKey,
		Address:    p.Address,
		ServerPub:  serverPub,
		Endpoint:   s.wgEndpoint(),
		AllowedIPs: s.wgAllowedIPs(),
		RealmIP:    wg.TunnelIP(s.cfg.WGServerAddr),
	}
}

func (s *Server) wgEndpointPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	if !s.cfg.WGEnabled {
		s.flashRedirect(w, r, "/staff", "error", "VPN is not enabled.")
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	raw := strings.TrimSpace(r.FormValue("endpoint"))
	if raw == "" {
		if err := s.id.Store().SetWGEndpoint(r.Context(), ""); err != nil {
			s.flashRedirect(w, r, "/staff#vpn", "error", "Could not clear the VPN endpoint.")
			return
		}
		s.logStaff(sess.User.Username, "wg-endpoint", "(default)")
		s.flashRedirect(w, r, "/staff#vpn", "success", "VPN endpoint reset to "+s.wgEndpoint()+".")
		return
	}
	ep, err := wg.NormalizeEndpoint(raw, s.cfg.WGPort)
	if err != nil {
		s.flashRedirect(w, r, "/staff#vpn", "error", err.Error())
		return
	}
	if err := s.id.Store().SetWGEndpoint(r.Context(), ep); err != nil {
		s.flashRedirect(w, r, "/staff#vpn", "error", "Could not save the VPN endpoint.")
		return
	}
	s.logStaff(sess.User.Username, "wg-endpoint", ep)
	s.flashRedirect(w, r, "/staff#vpn", "success", "VPN endpoint set to "+ep+". New and re-downloaded configs use this.")
}

func (s *Server) wgPeerViews(peers []identity.WGPeer, serverPub string) []wgPeerView {
	out := make([]wgPeerView, 0, len(peers))
	for _, p := range peers {
		v := wgPeerView{WGPeer: p}
		if qr, err := identity.QRDataURI(wg.ClientConf(s.wgClientOpts(p, serverPub))); err == nil {
			v.QR = template.URL(qr)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) wgCreatePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.wgOn() {
		s.flashRedirect(w, r, "/account", "error", "VPN configs are not enabled.")
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.ticketRL.Allow(s.ip(r) + ":wg:" + sess.User.Username) {
		s.flashRedirect(w, r, "/account#vpn", "error", "Too many VPN requests. Wait and try again.")
		return
	}
	if _, err := wg.EnsureServerKeys(s.cfg.WGDir); err != nil {
		s.log.Error("wg keys", "err", err)
		s.flashRedirect(w, r, "/account#vpn", "error", "Could not prepare VPN keys.")
		return
	}
	used, err := s.id.Store().UsedWGAddresses(r.Context())
	if err != nil {
		s.flashRedirect(w, r, "/account#vpn", "error", "Could not allocate a VPN address.")
		return
	}
	addr, err := wg.NextAddress(used)
	if err != nil {
		s.flashRedirect(w, r, "/account#vpn", "error", err.Error())
		return
	}
	kp, err := wg.Generate()
	if err != nil {
		s.flashRedirect(w, r, "/account#vpn", "error", "Could not generate a VPN key.")
		return
	}
	name := r.FormValue("name")
	peer, err := s.id.Store().InsertWGPeer(r.Context(), sess.User.ID, name, kp.Public, kp.Private, addr, s.cfg.WGPeerMax)
	if err != nil {
		s.flashRedirect(w, r, "/account#vpn", "error", err.Error())
		return
	}
	if err := wg.WritePeerFile(s.cfg.WGDir, peer.ID, sess.User.ID, peer.PublicKey, peer.Address); err != nil {
		_ = s.id.Store().DeleteWGPeer(r.Context(), sess.User.ID, peer.ID)
		s.log.Error("wg peer file", "err", err)
		s.flashRedirect(w, r, "/account#vpn", "error", "Could not publish the VPN peer.")
		return
	}
	s.log.Info("wg peer add", "user", sess.User.Username, "id", peer.ID, "addr", peer.Address)
	s.flashRedirect(w, r, "/account#vpn", "success", "VPN config created. Scan the QR or download the bundle.")
}

func (s *Server) wgDeletePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.wgOn() {
		s.flashRedirect(w, r, "/account", "error", "VPN configs are not enabled.")
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id < 1 {
		s.flashRedirect(w, r, "/account#vpn", "error", "VPN config not found.")
		return
	}
	if err := s.id.Store().DeleteWGPeer(r.Context(), sess.User.ID, id); err != nil {
		s.flashRedirect(w, r, "/account#vpn", "error", "VPN config not found.")
		return
	}
	_ = wg.RemovePeerFile(s.cfg.WGDir, id)
	s.log.Info("wg peer revoke", "user", sess.User.Username, "id", id)
	s.flashRedirect(w, r, "/account#vpn", "success", "VPN config revoked. The old file will no longer connect.")
}

func (s *Server) wgDownload(w http.ResponseWriter, r *http.Request) {
	sess := s.requireLogin(w, r)
	if sess == nil {
		return
	}
	if !s.wgOn() {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	peer, err := s.id.Store().GetWGPeer(r.Context(), sess.User.ID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	keys, err := wg.EnsureServerKeys(s.cfg.WGDir)
	if err != nil {
		http.Error(w, "VPN is not ready.", http.StatusServiceUnavailable)
		return
	}
	opts := s.wgClientOpts(peer, keys.Public)
	kind := r.PathValue("kind")
	stem := strings.ToLower(strings.ReplaceAll(s.cfg.RealmName, " ", "-")) + "-" + peer.Name
	switch kind {
	case "zip":
		body, err := wg.BundleZip(opts)
		if err != nil {
			http.Error(w, "Could not build the bundle.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeWGFile(stem)+`.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	case "conf":
		body := wg.ClientConf(opts)
		w.Header().Set("Content-Type", "application/x-wireguard-profile")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeWGFile(stem)+`.conf"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(body))
	default:
		http.NotFound(w, r)
	}
}

func safeWGFile(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "wireguard"
	}
	return out
}
