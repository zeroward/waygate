#!/bin/sh
# File Gatehouse security findings as GitHub issues.
# Requires: gh auth login  (repo zeroward/waygate)
set -eu
cd "$(dirname "$0")/.."

if ! command -v gh >/dev/null 2>&1; then
  echo "gh not in PATH" >&2
  exit 1
fi

ensure_label() {
  name=$1
  color=$2
  desc=$3
  gh label create "$name" --color "$color" --description "$desc" 2>/dev/null || true
}

ensure_label security "B60205" "Security finding"
ensure_label P0 "B60205" "Fix immediately"
ensure_label P1 "D93F0B" "High"
ensure_label P2 "FBCA04" "Medium"
ensure_label P3 "0E8A16" "Low / hygiene"

issue() {
  title=$1
  labels=$2
  body=$3
  gh issue create --title "$title" --label "$labels" --body "$body"
}

issue "security: un-publish MySQL 3306 (S0)" "security,P0" "$(cat <<'EOF'
## Severity
Critical (live deploy)

## Surface
`ac-database` is published `0.0.0.0:3306`. firewalld public does not list 3306, but Docker still binds all interfaces and adds a `DOCKER` ACCEPT for that port. Auth salt/verifier and character data live here.

Observed 2026-08-30 on the Chunguscraft host (`docker ps`, `ss -lntp`).

## Gameplan
1. Remove the host port mapping for MySQL in AzerothCore compose (or bind `127.0.0.1:3306:3306` only).
2. Recreate `ac-database`. Confirm `ss` no longer shows `0.0.0.0:3306`.
3. Rotate MySQL root and `webreg` passwords.
4. Keep `MYSQL_HOST=ac-database` on `ac-network` only.

## Accept
`docker ps` does not list `0.0.0.0:3306`.

Do not merge to main until reviewed. Do not paste DB passwords into this thread.
EOF
)"

issue "security: session cookie missing Secure and HSTS (S1)" "security,P1" "$(cat <<'EOF'
## Severity
High (live config)

## Surface
`SESSION_SECURE_COOKIE=false`. Cookie is HttpOnly + SameSite=Lax only (`internal/session/session.go`). HSTS is only set when Secure is on (`internal/web/middleware.go`). Origin `GET http://127.0.0.1:3080/account` confirmed no `Secure` flag. Compose publishes `3080:3080`.

## Gameplan
1. Confirm public access is HTTPS-only (Cloudflare tunnel).
2. Set `SESSION_SECURE_COOKIE=true` and recreate waygate.
3. Warn at boot if `SITE_URL` is https but Secure is false.
4. Prefer not publishing host 3080.

## Accept
`Set-Cookie` includes `Secure`. HTTPS has HSTS.

Do not merge to main until reviewed.
EOF
)"

issue "security: TRUST_PROXY with published origin bypasses rate limits (S2)" "security,P1" "$(cat <<'EOF'
## Severity
High (live config)

## Surface
`TRUST_PROXY=true`. `internal/web/ip.go` trusts the first `X-Forwarded-For` / `X-Real-IP`. Register, login, reset, contact, TOTP, and passkey rate limits key off that IP. Origin `:3080` is published on `0.0.0.0`.

## Gameplan
- If Cloudflare is the only ingress: stop publishing 3080; keep TrustProxy.
- If origin stays open: `TRUST_PROXY=false`, or only trust Cloudflare ranges.
- Test: TrustProxy false → forged XFF does not change the rate-limit key.

Do not merge to main until reviewed.
EOF
)"

issue "security: un-publish worldserver SOAP 7878 (S3)" "security,P1" "$(cat <<'EOF'
## Severity
High (live deploy)

## Surface
`ac-worldserver` publishes `0.0.0.0:7878`. SOAP is gmlevel 3 and can create accounts / set passwords. In-app quoting is fine (`internal/soap/soap.go`); the host bind is the bug. firewalld public does not list 7878; Docker still ACCEPTs it.

## Gameplan
1. Remove `DOCKER_SOAP_EXTERNAL_PORT` host publish. Leave SOAP on `ac-network` (`SOAP_HOST=ac-worldserver`).
2. Recreate worldserver. Confirm `ss` has no `0.0.0.0:7878`.
3. Rotate the SOAP password if it has ever been pasted in chat.
4. Do not open 7878/tcp in firewalld.

## Accept
waygate can still `account create` over the docker network. 7878 is not on `0.0.0.0`.

Do not merge to main until reviewed. Do not paste the SOAP password here.
EOF
)"

issue "security: enable ClamAV and stop publishing .exe (S4)" "security,P1" "$(cat <<'EOF'
## Severity
High

## Surface
`internal/web/server.go` logs `clamav scanning disabled` and never calls `SetScanner`. `allowedExt` includes `.exe`, `.rar`, `.zip` (`internal/downloads/catalog.go`). Staff (`GM_MIN_LEVEL` default 1) can upload up to 20 GiB; any logged-in player can download.

## Gameplan
1. `SetScanner(clamav.New(...))` when `CLAMAV_ADDR` is set; fail closed if clamd is down.
2. Disallow `.exe` unless you truly ship a patched Wow.exe.
3. Re-scan existing `downloads/`.
4. Pin SHA-256 for the huge client zip instead of skipping over `CLAMAV_SCAN_MAX_MB`.
5. Filename sanitizer for Windows `Kopie` names is S12 — do not block those while tightening types.

## Accept
A test upload is scanned. Scanner-down rejects the publish. `.exe` is rejected (or explicitly documented).

Do not merge to main until reviewed.
EOF
)"

issue "security: restrict WireGuard FORWARD to realm ports (S5)" "security,P1" "$(cat <<'EOF'
## Severity
High

## Surface
`waygate-wg` uses `network_mode: host` and `NET_ADMIN`. `ensureForward` in `internal/wg/agent.go` ACCEPTs all FORWARD in/out `wg0` plus MASQUERADE. Client AllowedIPs correctly omit `0.0.0.0/0`, but a hostile peer can still use the **server** as a hop to LAN/other docker nets.

Repo-root `server.key` / `client1.key` exist on disk (not in git history).

## Gameplan
1. FORWARD allowlist: auth/world/site (+ explicit extra CIDRs only). Default DROP.
2. Delete stray keys in the repo root. Rotate WG keys if those files ever left the box.
3. Keep client AllowedIPs split-tunnel.

## Accept
`iptables -S FORWARD` no longer has bare `-i wg0 -j ACCEPT` / `-o wg0 -j ACCEPT`. Realm login still works over WG.

Do not merge to main until reviewed.
EOF
)"

issue "security: raise GM_MIN_LEVEL so GM 1 is not a full admin (S6)" "security,P1" "$(cat <<'EOF'
## Severity
High (authz)

## Surface
`GM_MIN_LEVEL=1` (default and `.env.example`). `/staff` can create accounts, reset passwords, ban, upload downloads, set the register key and VPN endpoint. KB edit is already GM 3+.

## Gameplan
Set live `GM_MIN_LEVEL=3`, or split “mod” (tickets only) vs “admin” (accounts/uploads). Default the example env to 3.

## Accept
A gmlevel 1 account cannot open `/staff` mutations. GM 3 still can. Super GM still cannot be granted from the site.

Do not merge to main until reviewed.
EOF
)"

issue "security: do not store TOTP secret in the session blob (S7)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
`totpStartPOST` copies secret, otpauth URL, and QR data URI onto the session (`internal/web/totp_handlers.go`). Sessions are JSON in `http_sessions` (`internal/session/session.go`). `StartTOTP` already writes the secret to `user_totp`.

Anyone who can read `data/kb.sqlite` gets in-progress TOTP secrets. Recovery codes stay in the session until the next navigation.

## Gameplan
Session holds “enroll pending” only. Render QR from `user_totp`. Clear recovery codes from the session after they are shown. `chmod 0600` the sqlite file.

Do not merge to main until reviewed.
EOF
)"

issue "security: step-up auth for password change and TOTP enroll (S8)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
Website password change needs the old password only. TOTP start does not re-ask the password. Disable TOTP already requires a code. A stolen session cookie (see S1) can enroll 2FA or rotate the password.

## Gameplan
Require TOTP (if enabled) on password change. Require password re-entry to start TOTP.

Do not merge to main until reviewed.
EOF
)"

issue "security: column-level UPDATE grant on characters (S9)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
`docs/mysql-grants.sql` grants `UPDATE` on all of `acore_characters.characters`. Unstuck only needs position/map/zone/homebind. A future query bug becomes a full character-edit primitive.

## Gameplan
Column-level UPDATE for unstuck fields only. Prefer SOAP when it is up. Apply on the live `webreg` user.

Do not merge to main until reviewed.
EOF
)"

issue "security: rate-limit client zip downloads (S10)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
`GET /downloads/{id}` is any logged-in user, `http.ServeFile`, no concurrency/byte cap (`internal/web/handlers.go`). A few accounts can saturate the host on a multi-GB client.

## Gameplan
Per-IP/account concurrency cap or 429, or host the client elsewhere. Keep the existing download logs.

Do not merge to main until reviewed.
EOF
)"

issue "security: single hostname for passkeys and SITE_URL (S11)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
WebAuthn RP ID is the `SITE_URL` host. Live logs have used `ccraft-signup.jonesfamily.casa` while the realm/VPN name is `ccraft.jonesfamily.casa`. Passkeys will not work across hosts.

## Gameplan
Pick one public hostname. Set `SITE_URL` and the RP ID to that host; HTTP-redirect the other.

Do not merge to main until reviewed.
EOF
)"

issue "security: allow Windows Kopie upload names (S12)" "security,P3" "$(cat <<'EOF'
## Severity
Low (staff UX, not a vuln)

## Surface
`SanitizeFileName` (`internal/downloads/manage.go`) uses a tight ASCII regex and rejects more than four dots. `addon 3.3.5 Compatible - Kopie.rar` (Windows copy) fails with “File name is not allowed.” Unicode dashes also fail.

## Gameplan
Normalize dashes/spaces; replace leftover runes with `-`; keep the extension allow-list. Tests for this name and `WoW-3.3.5a.zip`.

Do not merge to main until reviewed.
EOF
)"

issue "security: regenerate session when entering TOTP challenge (S13)" "security,P3" "$(cat <<'EOF'
## Severity
Low

## Surface
Password success with TOTP sets `PendingUser` on the existing session id (`internal/web/handlers.go` loginPOST). Regenerate happens after the code (`totp_handlers.go`). The window still needs TOTP, but regenerating at password success is cheaper.

## Gameplan
`Regenerate` when entering the TOTP challenge; copy only `PendingUser` / `PendingNext`.

Do not merge to main until reviewed.
EOF
)"

issue "security: drop capabilities on the waygate container (S14)" "security,P3" "$(cat <<'EOF'
## Severity
Low

## Surface
waygate is already `read_only` + non-root (`docker-compose.yml`). Missing `cap_drop: ALL` and `security_opt: no-new-privileges:true`. Do **not** apply that to `wireguard` (needs `NET_ADMIN`).

## Gameplan
Add those two settings to the `waygate` service only.

Do not merge to main until reviewed.
EOF
)"

issue "security: website login ignores game bans (S15)" "security,P1" "$(cat <<'EOF'
## Severity
High

## Surface
`identity.Authenticate` only checks bans on the legacy SRP6 claim path (`internal/identity/service.go`). After Argon2id is set, password login never looks at `account_banned`. Passkey login never does either. Staff Suspend blocks Wow.exe, not Gatehouse (tickets, downloads, VPN, and `/staff` if `staff_level` remains).

## Gameplan
After password/passkey success, reject if any linked WoW account has an active ban. Use the same generic error as a bad password. Tests: ban → website login denied; unban → allowed.

Do not merge to main until reviewed.
EOF
)"

issue "security: /staff/rank does not update website staff_level (S16)" "security,P1" "$(cat <<'EOF'
## Severity
High (authz)

## Surface
Session privileges use `users.staff_level`. `/staff/rank` only writes AzerothCore `account_access`. `SetStaffLevel` exists and is never called from HTTP. Demote Admin→Player: in-game GM cleared, **website admin retained**. Promote the other way: in-game GM, website still locked out.

## Gameplan
On every successful `SetGMLevel`, set the linked identity `staff_level` to the same value (still never Super GM). Reload staff from DB on `/staff*` or destroy that user’s sessions on demotion. Test: demote → `/staff` 403.

Do not merge to main until reviewed.
EOF
)"

issue "security: TOTP setup POST disables live MFA (S17)" "security,P1" "$(cat <<'EOF'
## Severity
High

## Surface
`StartTOTP` upserts `enabled = 0` and clears recovery hashes (`internal/identity/totp.go`). `totpStartPOST` does not require TOTP to be off. The UI hides Setup when enabled; a direct authenticated POST (stolen session) turns MFA off until someone confirms a new secret.

## Gameplan
If TOTP is already enabled, reject start unless a current code is supplied. Store a pending secret beside the live row; only set `enabled=1` on confirm. Test: enabled → start leaves `enabled=1`.

Do not merge to main until reviewed.
EOF
)"

issue "security: password change does not invalidate other sessions (S18)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
Email reset and password change update the hash only. Existing SQLite sessions remain valid for up to 24h (`internal/session/session.go`).

## Gameplan
Persist `user_id` on `http_sessions`. On password reset/change and staff demotion, delete that user’s sessions (optionally keep the current one after regenerate).

Do not merge to main until reviewed.
EOF
)"

issue "security: email verify should not be a GET (S19)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
`GET /account/verify/{token}` consumes the token and creates the account (`internal/web/handlers.go`). Mail prefetch/scanners can activate registrations.

## Gameplan
Show a confirm page and consume the token on POST. Keep hashed single-use tokens.

Do not merge to main until reviewed.
EOF
)"

issue "security: strengthen TOTP recovery codes (S20)" "security,P2" "$(cat <<'EOF'
## Severity
Medium

## Surface
Recovery codes are 5 random bytes (10 hex ≈ 40 bits) compared with string equality, not constant-time (`internal/identity/totp.go`). Enroll confirm/disable are not rate-limited.

## Gameplan
Longer codes, `subtle.ConstantTimeCompare`, rate-limit confirm/disable.

Do not merge to main until reviewed.
EOF
)"

echo "Created security issues. List:"
gh issue list --label security --limit 30
