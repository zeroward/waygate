# Gatehouse security assessment

**Date:** 2026-08-30  
**Branch:** `security-analysis` (from `dev` @ `ef3596d`)  
**Scope:** Public-facing Gatehouse (waygate) HTTP, sessions, staff uploads, player VPN, SOAP as used by the site, MySQL grants, host publish/firewall.  
**Method:** Static review of Go handlers and config, plus observation-only live checks on this host (`ss`, `firewall-cmd`, `docker ps`, `curl` to origin). No exploit PoCs, no production attacks.

Do not copy `.env` secrets, SOAP passwords, or WireGuard private keys into git or GitHub issues.

## Executive summary

The app’s *code* is careful in several places that usually go wrong (parameterized SQL, CSRF, owner checks on guid/tickets/unstuck, no Super GM from the site, SOAP quoting). The *deployment* is what will get you first:

1. **MySQL is published on `0.0.0.0:3306`.** Firewalld public does not list 3306, but Docker still binds all interfaces and installs a `DOCKER` ACCEPT for that port. Treat as exposed until you un-publish it.
2. **SOAP `7878` is published the same way** (`ac-worldserver`). That is a gmlevel-3 account-create/password API.
3. **Session cookies are not `Secure`** and there is **no HSTS**, while the public site is HTTPS. Origin `:3080` is also published on all interfaces.
4. **ClamAV is installed and unused.** Staff can upload `.exe` / archives; every logged-in player can download them.
5. **WireGuard on the host forwards all `wg0` traffic**, not just realm ports.
6. **A game ban does not log the player out of the website** (password + passkey). Demoting GM in the panel does not clear website `staff_level`. Re-POSTing TOTP setup sets `enabled=0` immediately.

hCaptcha **is** on `/register` (live). Companion/unstuck/ticket IDORs were **not** found.

---

## Live evidence (this host)

| Check | Result |
| --- | --- |
| `docker ps` publishes | `waygate :3080`, `ac-worldserver :7878` and `:28085`, `ac-authserver :3724`, **`ac-database :3306`**, all `0.0.0.0` |
| firewalld zone `public` ports | `8080/tcp`, `3724/tcp`, `8085/tcp`, `51820/udp` — **not** 3080, 7878, 3306, 28085 |
| firewalld services | `ssh`, `mdns`, `dhcpv6-client` |
| Docker `DOCKER` chain | ACCEPT for host-published **3080, 7878, 3724, 8085, 3306** |
| iptables `INPUT` policy | `ACCEPT` |
| WG FORWARD | `-A FORWARD -i wg0 -j ACCEPT` and `-o wg0 -j ACCEPT` |
| Origin cookie `GET http://127.0.0.1:3080/account` | `waygate_session=…; Path=/; Max-Age=86400; HttpOnly; SameSite=Lax` — **no `Secure`**, **no HSTS** |
| `/register` | hCaptcha widget present |
| `git log --all` for `*.key` / `data/wg/*` | nothing committed |
| GitHub CLI | installed locally; **not authenticated** — issues are drafted in `scripts/file-security-issues.sh` |

Docker + firewalld often disagree. Public-zone “no” is **not** enough while `0.0.0.0:3306` and `0.0.0.0:7878` exist. Un-publish those ports.

---

## Findings

Severity: **Critical / High / Medium / Low**. IDs match GitHub issue drafts (S0–S20).

### S0 — Critical — MySQL published on all interfaces

**Surface:** `ac-database` `0.0.0.0:3306->3306/tcp`.  
**Why it matters:** Auth `salt`/`verifier`, character rows, and the `webreg` account live here. If the port is reachable, brute-force or a leaked `.env` password is a full realm compromise.  
**Gameplan:**

1. Stop publishing 3306. In AzerothCore compose, remove `DOCKER_DB_EXTERNAL_PORT` host mapping (or bind `127.0.0.1:3306:3306` if you must debug locally).
2. Recreate `ac-database`. Confirm `ss -lntp` no longer shows `0.0.0.0:3306`.
3. Rotate the MySQL root and `webreg` passwords (they have been on this host in `.env`).
4. Leave clients on `ac-network` only (`MYSQL_HOST=ac-database`).

**Accept:** `docker ps` does not list `0.0.0.0:3306`. `firewall-cmd --query-port=3306/tcp` stays `no`.

### S1 — High — Session cookie not Secure; no HSTS

**Surface:** `SESSION_SECURE_COOKIE=false`; `internal/session/session.go` `writeCookie`; `internal/web/middleware.go` HSTS only if Secure. Origin `GET /account` cookie has HttpOnly + SameSite=Lax only. Compose publishes `3080:3080`.  
**Gameplan:** Confirm players only use HTTPS. Set `SESSION_SECURE_COOKIE=true`, recreate waygate. Add a boot warning if `SITE_URL` is `https://` but Secure is false. Prefer not publishing 3080 (Cloudflare tunnel only).

**Accept:** `Set-Cookie` includes `Secure`. HTTPS responses include HSTS. HTTP origin is not on the public NIC.

### S2 — High — TRUST_PROXY + published origin

**Surface:** `TRUST_PROXY=true`; `internal/web/ip.go` trusts first `X-Forwarded-For` / `X-Real-IP`. Rate limits for register/login/reset/contact/TOTP/passkey use that IP.  
**Gameplan:** If `:3080` is only reached via Cloudflare, keep TrustProxy but **do not** publish 3080 on `0.0.0.0`. If origin must stay open, set `TRUST_PROXY=false` or ignore XFF except from Cloudflare ranges. Test: with TrustProxy false, a forged XFF does not change the rate-limit key.

### S3 — High — SOAP 7878 published on the host

**Surface:** `ac-worldserver 0.0.0.0:7878`; SOAP.IP is `0.0.0.0` so the waygate container can connect. SOAP account is gmlevel 3 (`account create`, `account set password`, bans). In-app quoting is fine; the bind is the bug.  
**Gameplan:** Remove the host publish (`DOCKER_SOAP_EXTERNAL_PORT`). Keep SOAP on `ac-network` only (`SOAP_HOST=ac-worldserver`). Confirm firewalld stays closed. Rotate the SOAP password if it has ever been pasted in chat.

**Accept:** `ss` has no `0.0.0.0:7878`. waygate can still create accounts via SOAP on the docker network.

### S4 — High — ClamAV never scans; `.exe` allowed

**Surface:** `internal/web/server.go` logs `clamav scanning disabled` and never `SetScanner`. `allowedExt` includes `.exe`, `.rar`, `.zip`. Staff (`GM_MIN_LEVEL` default 1) may upload up to 20 GiB; any login can `GET /downloads/{id}`.  
**Gameplan:** Call `SetScanner` when `CLAMAV_ADDR` is set; fail closed if clamd is down. Drop `.exe` unless you really ship a patched client as `.exe`. Re-scan existing `downloads/`. Pin SHA-256 for the huge client zip instead of skipping it (`CLAMAV_SCAN_MAX_MB`).

### S5 — High — WireGuard host forwards everything

**Surface:** `waygate-wg` `network_mode: host`, `NET_ADMIN`. `ensureForward` ACCEPT all FORWARD in/out `wg0` plus MASQUERADE. Client AllowedIPs are split-tunnel (good). A hostile peer can still use the **server** as a hop to whatever the kernel routes (LAN, other containers). Repo-root `server.key` / `client1.key` exist on disk (not in git).  
**Gameplan:** FORWARD allowlist: auth/world/site (and explicit extra CIDRs only); default DROP. Delete stray keys in the repo root. Rotate WG keys if those files ever left the box. Do not add `0.0.0.0/0` to clients.

### S6 — High — GM 1 is a full Admin panel

**Surface:** `GM_MIN_LEVEL=1`. `/staff` can create accounts, reset passwords, ban, upload downloads, set register key and VPN endpoint. KB edit is correctly GM 3+.  
**Gameplan:** Set live `GM_MIN_LEVEL=3`, or split “mod” (tickets only) vs “admin” (accounts/uploads). Default the example env to 3.

### S7 — Medium — TOTP secret stored in the session blob

**Surface:** `totpStartPOST` copies secret, otpauth URL, and QR data URI onto the session; `http_sessions.data` is JSON in `data/kb.sqlite`. `StartTOTP` already writes the secret to `user_totp`.  
**Gameplan:** Session flag “enroll pending” only. Render QR from `user_totp`. Clear recovery codes from the session after display. `chmod 0600` the sqlite file.

### S8 — Medium — No step-up for password change / TOTP enroll

**Surface:** Password change needs the old password only. TOTP start does not re-ask the password. Disable TOTP *does* require a code. A stolen cookie (S1) can enroll or change password.  
**Gameplan:** Require TOTP (if enabled) on password change. Require password re-entry to start TOTP.

### S9 — Medium — UPDATE all columns on `characters`

**Surface:** `docs/mysql-grants.sql` `GRANT UPDATE ON acore_characters.characters`. Unstuck only needs position/map/zone/homebind.  
**Gameplan:** Column-level UPDATE for unstuck fields. Prefer SOAP when it is up.

### S10 — Medium — No rate limit on client zip download

**Surface:** `GET /downloads/{id}` → `http.ServeFile`, login only.  
**Gameplan:** Per-IP/account concurrency cap or 429; or host the client elsewhere. Keep download logs.

### S11 — Medium — Passkey RP ID vs public hostname

**Surface:** WebAuthn RP ID = `SITE_URL` host. Logs have used `ccraft-signup.jonesfamily.casa` while the realm/VPN name is `ccraft.jonesfamily.casa`. Passkeys do not follow you across hosts.  
**Gameplan:** One public hostname. Set `SITE_URL` / RP ID to that host; redirect the other.

### S12 — Low — Upload name `addon 3.3.5 Compatible - Kopie.rar`

**Surface:** `SanitizeFileName` ASCII regex + `dots > 4`. Windows “ - Kopie” and unicode dashes → “File name is not allowed.” Not a vuln; it blocks staff.  
**Gameplan:** Normalize dashes/spaces; replace leftover runes with `-`; keep the extension allow-list. Test this name and `WoW-3.3.5a.zip`.

### S13 — Low — TOTP pending session not regenerated

**Surface:** Password success with TOTP sets `PendingUser` on the same session id; regenerate happens after the code.  
**Gameplan:** `Regenerate` when entering the TOTP challenge; copy only `PendingUser` / `PendingNext`.

### S15 — High — Suspended accounts still use the website

**Surface:** `identity.Authenticate` checks bans only on the legacy SRP6 claim path. After Argon2id is set, password login never looks at `account_banned`. Passkey login never does either. Staff Suspend blocks Wow.exe, not Gatehouse (tickets, downloads, VPN, possibly `/staff` if `staff_level` remains).  
**Gameplan:** After password/passkey success, reject if any linked WoW account has an active ban. Same generic error as a bad password. Tests: ban → website login denied; unban → allowed.

### S16 — High — Website staff rank does not follow `/staff/rank`

**Surface:** Session `GMLevel` is `users.staff_level`. `/staff/rank` only writes AzerothCore `account_access`. `SetStaffLevel` exists and is never called from HTTP. Demote Admin→Player in the panel: in-game GM gone, **website admin remains**. Promote the other way: in-game GM, website still locked out until something else sets `staff_level`.  
**Gameplan:** On every successful `SetGMLevel`, set linked `staff_level` to the same value (still never 4). Reload staff from DB on `/staff*` or destroy that user’s sessions on demotion. Test: demote → `/staff` 403.

### S17 — High — Starting TOTP turns existing MFA off

**Surface:** `StartTOTP` upserts `enabled = 0` and wipes recovery hashes. `totpStartPOST` does not require TOTP to be off. The UI hides Setup when enabled; a direct POST (stolen session) disables MFA until someone confirms a new secret. Password login then skips the second factor.  
**Gameplan:** If TOTP is already enabled, reject start unless a current code is supplied. Store a pending secret beside the live one; only flip `enabled` on confirm. Test: enabled → start leaves `enabled=1`.

### S18 — Medium — Password change/reset does not kill other sessions

**Surface:** Email reset and password change update the hash only. SQLite sessions stay valid up to 24h.  
**Gameplan:** Index sessions by `user_id`. On reset/change/demotion, delete that user’s sessions (optionally keep the current one after regenerate).

### S19 — Medium — Email verify is a state-changing GET

**Surface:** `GET /account/verify/{token}` consumes the token and creates the account. Prefetch/mail scanners can activate it.  
**Gameplan:** Interstitial page + POST, token in the form. Keep hashed single-use tokens.

### S20 — Medium — Weak TOTP recovery codes

**Surface:** Recovery codes are 5 random bytes (10 hex ≈ 40 bits) compared with `==`, not constant-time. Enroll confirm/disable are not rate-limited.  
**Gameplan:** Longer codes, `subtle.ConstantTimeCompare`, rate-limit confirm/disable.

### S14 — Low — Compose hardening

**Surface:** waygate is already `read_only` + non-root. Missing `cap_drop: ALL` and `no-new-privileges`. Do not apply that to `wireguard`.  
**Gameplan:** Add those to the `waygate` service only.

---

## Checked and not ticketed as vulns

| Area | Result |
| --- | --- |
| SQL values | Parameterized. Database names restricted to `[A-Za-z0-9_]` |
| CSRF | HTML POST `csrf_token`; passkey JSON `X-CSRF-Token` |
| Companion / unstuck / tickets / WG peers | Owner-scoped; unknown guid 404 |
| Armory by name | Login-gated inspect of any toon — by design, not IDOR |
| `next=` | `safeNext` rejects `//`, `\`, `://` |
| Markdown | HTML escaped; CSP `script-src 'self'` |
| SOAP command building | Quoted; `"` rejected; passwords not logged |
| Super GM (4) | Cannot be granted from the site |
| Password reset | 32-byte token, SHA-256 stored, 15 min, single-use, generic response |
| Registration | hCaptcha live |
| WG client AllowedIPs | `0.0.0.0/0` stripped |
| Download path traversal | `SafeRelPath` / `ResolveUnder` |

---

## Suggested fix order

1. Un-publish **3306** and **7878** (S0, S3) — ops, same day.  
2. `SESSION_SECURE_COOKIE=true` and stop publishing **3080** if Cloudflare is the only ingress (S1, S2).  
3. Enable ClamAV + drop `.exe` (S4); raise `GM_MIN_LEVEL` (S6).  
4. Tighten WG FORWARD (S5).  
5. Ban check on website login; sync `staff_level`; stop TOTP start from disabling MFA (S15–S17).  
6. Session/TOTP and grant nits (S7–S9, S13, S18–S20).  
7. Download limits, passkey host, filename, compose caps (S10–S12, S14).

Code remediations are follow-up PRs on this branch or stacked branches. This document does not merge to `main` by itself.

## Filing GitHub issues

`gh` is not logged in on this host. After `gh auth login`:

```bash
./scripts/file-security-issues.sh
```

That creates S0–S20 on `zeroward/waygate` with labels `security` and `P0`/`P1`/`P2`/`P3`.
