# Gatehouse: what to fix to secure the app

Assessment date: **2026-09-06**. Baseline: **c87fe43dc24b0f996bf92e3073598bb74641a586**, including the pre-existing edits to `internal/wg/agent.go`, its untracked test, and `downloads/catalog.json`. Their SHA-256 hashes are in [baseline.json](security-assessment-2026-09-06/baseline.json). Application behavior was not changed.

## Fix these first

1. **Separate password verification from credential encryption.** A database copy currently contains everything needed to decrypt saved game passwords, without guessing a website password.
2. **Make bans, password resets and demotions invalidate every authentication state.** Pending MFA sessions survive revocation; KB editing trusts an old role.
3. **Synchronize session access.** The existing race-enabled suite fails when staff requests overlap session persistence.
4. **Upgrade the Go build toolchain and affected dependencies.** The app builds with Go 1.23; current vulnerability checks report affected standard-library functions and a JWT dependency used through WebAuthn.
5. **Close the remaining upload and VPN gaps.** Oversized files bypass scanning, repeated multipart file fields leak staging files, and VPN rules do not limit destination hosts or protect host-local services.

These are app-hardening recommendations, not evidence that anyone has compromised the app. Severity below describes impact **with the stated prerequisites**, not an assumption that every issue is remotely exploitable.

## What was checked

The review covered 76 registered HTTP routes, authentication and identity storage, staff and player authorization, uploads/downloads, templates and browser code, SOAP/MySQL integration, outbound requests, the WireGuard agent, Docker/CI configuration, and locally available Git history. [Route inventory](security-assessment-2026-09-06/routes.json) records direct guard calls; inline and delegated checks were reviewed separately.

| Check | Result and practical limit |
| --- | --- |
| `go test ./...` on isolated baseline, Go 1.23.12 | Passed. [Output](security-assessment-2026-09-06/baseline-test.txt) |
| `go vet ./...` on isolated baseline | Passed; output empty. [Output](security-assessment-2026-09-06/vet.txt) |
| `go test -race ./...` on isolated baseline | Failed in `TestStaffCreateAndReset` and `TestStaffBanAndUnban`; session serialization races with role updates. [Traces](security-assessment-2026-09-06/race.txt) |
| Added synthetic reproduction tests | All 13 tests passed, meaning they reproduced the baseline behavior they assert. One concurrency test requires `-race` to detect its race; the existing baseline suite already supplied independent race evidence. [Output](security-assessment-2026-09-06/reproductions.txt) |
| Bounded path-parser fuzzing | 909,492 executions over approximately 10 seconds, two workers, no lexical traversal invariant failure. This does **not** cover symlinks. [Output](security-assessment-2026-09-06/fuzz.txt) |
| `govulncheck v1.7.0 -show verbose ./...` | Completed against Go 1.23.12 and the baseline dependencies: 38 symbol-level alerts, six package-level alerts, 26 module-level alerts. These counts are scanner results, **not 38 confirmed app exploits**. [Full output](security-assessment-2026-09-06/govulncheck.txt) |
| Secrets/history scan | Custom redacted regex scan of 486 reachable Git blobs. 56 candidates were placeholders, empty example settings or source assignments; no actual credential identified by those rules. No `.env`, `*.key` or `data/wg` history found. [Metadata](security-assessment-2026-09-06/secret-scan.json) |

Tests used synthetic identities, temporary databases and local test servers. The supplied Compose stacks were not started. No real database contents, `.env` values or private-key values were read into evidence. File permission metadata was inspected. Test dependencies and the vulnerability database were fetched over the network; production integrations were not contacted.

## Confirmed problems and fixes

### A01 — High: stored password verifier also decrypts game credentials

**Where:** `internal/identity/hash.go:29`, `internal/identity/service.go:60`.

`HashPasswordAndKey` stores the Argon2 output in `users.password_hash` and returns the same bytes as the key-encryption key. The encrypted DEK and game-password ciphertext also live in SQLite. Someone with a readable database or backup can decode the last hash component, unwrap the DEK, and decrypt saved game passwords. `TestAssessmentStoredHashDecryptsClientPassword` demonstrated this using only stored fields after fixture creation.

**Fix:** Use independent, domain-separated password-verification and encryption derivations; do not store the encryption derivation output. Version the wrapping format and migrate credentials under a newly derived wrapping key after successful password authentication. A reset without the old password must explicitly invalidate saved secrets and allow re-entry, rather than leave a broken wrap. The existing `SetPassword` reset path changes the password hash without rewrapping the DEK, which already makes saved credentials inaccessible after reset.

**Accept:** Database-only decryption fails; old/new password change preserves intended secrets; reset gives a clear re-entry path; migration is transactional and tested. If a database copy was exposed, wrapping migration alone cannot protect passwords present in that old copy.

### A02 — High: revocation misses pending MFA sessions

**Where:** `internal/session/session.go:210,227`, `internal/web/totp_handlers.go:11`.

Session persistence indexes only `sess.User`, and `RevokeUser` checks only that field. Password-accepted sessions instead hold `PendingUser`. Completing TOTP does not reload the identity or recheck bans. A user who starts login before a ban can complete it afterward with their valid second factor. The end-to-end synthetic test reproduced successful login after both a ban and session revocation. Password-reset revocation has the same pending-session gap.

**Fix:** Associate pending and authenticated sessions with the user; revoke both on security changes. Give pending authentication a short deadline and verify a current authentication version, role and ban state immediately before issuing the final session. Rotate the session ID when entering MFA.

**Accept:** Pending login fails after ban, password reset or revocation; expiry is enforced; ordinary password-plus-TOTP login still works.

### A03 — High: demoted admins retain KB privileges

**Where:** `internal/web/kb_handlers.go:24`, `internal/web/handlers.go:778`.

Staff guards refresh the current role, but `requireKBEditor` uses cached `sess.User.GMLevel`. Rank changes revoke sessions only when the new rank is Player. An Admin demoted to GM2 can continue KB writes through an existing session until another action refreshes it. The synthetic test persisted GM2 and confirmed the old session still passed the KB editor guard.

**Fix:** Use one current-role authorization path for every privileged handler; deny access if role lookup fails. Revoke or refresh all sessions on every privilege reduction, not just demotion to Player. Handle staff-level persistence errors rather than reporting success after ignoring them.

**Accept:** An existing Admin session immediately loses KB edit/delete/preview access after demotion to GM2 or GM1, while retaining allowed player/moderator features.

### A04 — Medium: concurrent requests race on shared sessions

**Where:** `internal/session/session.go`, `internal/web/handlers.go:375`, `internal/web/middleware.go:31`.

The store mutex protects the map but not the mutable session objects returned to handlers. Existing tests produced races between `applyStaffLevel` and JSON serialization in `SaveLatest`. User state, pending login, challenges, flashes and revocation flags share this design. Authorization corruption is a concern; an actual privilege escalation from the race was not demonstrated.

**Fix:** Introduce synchronized session reads/updates and persistence snapshots, with an atomic lifecycle for regeneration/revocation. Ensure a request finishing after revocation cannot recreate the revoked row.

**Accept:** The complete race-enabled suite passes, including concurrent requests on one cookie and overlapping logout/reset/demotion/persistence.

### A05 — High maintenance priority: outdated Go build and vulnerable dependency

**Where:** `Dockerfile:2`, `go.mod`, `.github/workflows/container.yml` and `Makefile`.

The available `golang:1.23-alpine` image runs Go 1.23.12. The scan flagged 37 standard-library symbol alerts and one JWT symbol alert. The JWT path is through WebAuthn registration to `Parser.ParseUnverified`; the advisory fixes v5 at **5.2.2**. Registration requires an authenticated session/CSRF and bounds JSON bodies to 64 KiB, so this is not an unrestricted anonymous JWT endpoint. [Go JWT advisory](https://pkg.go.dev/vuln/GO-2025-3553).

**Fix:** Move the Docker builder, CI and developer test runner together to a current patched supported Go release, then upgrade compatible dependencies and repeat the tests/scan. Go's official history lists **1.27.1** as released September 1, 2026; use that as the assessment-date target, validating compatibility before deployment. [Go release history](https://go.dev/doc/devel/release).

**Accept:** A freshly rebuilt image uses the intended patched toolchain; regression tests pass; remaining scanner alerts have documented app-specific dispositions. Do not count unused SSH/OpenPGP module alerts as exposed functionality: this app uses other `x/crypto` packages. Some standard-library traces are overapproximations, including Windows-only behavior on this Linux deployment and template contexts the app may not use. No dependency-exploit load tests were run.

### A06 — Medium: WebAuthn does not require user verification

**Where:** `internal/web/passkey_handlers.go:20,198`.

Registration and discoverable login request `VerificationPreferred`, and passkey login bypasses TOTP. A full synthetic registration/login succeeded with the authenticator's user-verification flag cleared. Someone able to use such a registered credential can log in using possession/user presence alone, without the expected PIN/biometric or TOTP. This is not a claim that all authenticators skip verification.

**Fix:** Require WebAuthn user verification for passwordless login and registration. Plan an enrollment transition for existing credentials that cannot meet it. Require recent password/MFA or an existing verified passkey before enrolling another credential; today a stolen authenticated session can register a persistent replacement login method without step-up.

**Accept:** UV=false registration/login fails; verified passkeys work; a session cookie alone cannot enroll a new credential. Wrong-origin, wrong-user and reused-challenge cases remain rejected.

### A07 — Medium: MFA failures can be treated as success or as disabled

**Where:** `internal/identity/totp.go:25,96`, `internal/identity/service.go:320`.

`TOTPStatus` turns all database errors into `false, nil`; callers then treat MFA as disabled. `ValidateTOTP` ignores errors saving consumed recovery codes and does not prevent reuse of a TOTP timestep. Synthetic tests showed a missing MFA table reported as disabled, and the same recovery code accepted twice when a fixture trigger rejected the consumption write. Concurrent recovery-code reads/updates are also non-atomic. Ban checks similarly discard `IsBanned` errors.

**Fix:** Distinguish missing enrollment from storage failure and fail authentication closed on lookup failure. Consume recovery codes atomically and require successful persistence. Track accepted timesteps for TOTP replay protection. Make enrollment state transitions atomic; the current start/check/upsert sequence can race confirmation. Apply account-based throttling to every second-factor/password-checking route, including password change and credential unlock.

**Accept:** Database failure cannot remove an MFA/ban requirement; one recovery code permits only one success under concurrency; reused timesteps fail; enrollment cannot overwrite enabled MFA in a race.

### A08 — Medium: file size bypasses antivirus scanning

**Where:** `internal/downloads/manage.go:138`, `internal/config/config.go:198`.

Files over the configured scan threshold are published as ready without calling the scanner. Defaults are a 100 MiB scan threshold versus a 20 GiB upload limit. A synthetic 4-byte threshold test published a larger file even with a scanner that always fails. Host-dropped files are also served without scanning, as documented. Requires an upload-authorized account or filesystem writer.

**Fix:** Quarantine unscanned uploads. Reject above-limit files unless a separate explicit review process approves an independently verified artifact. A hash computed from the uploaded file is not independent approval. Scan or explicitly approve host-dropped files too, and reconcile actual ClamAV stream/archive limits.

**Accept:** Clean supported uploads publish; infected, unavailable-scanner and over-limit uploads remain unavailable; no file becomes downloadable before its approval succeeds.

### A09 — Medium: repeated multipart file fields leak staging files

**Where:** `internal/web/staff_downloads.go:76`.

Each `file` field creates a new temporary file but overwrites the one `staged` variable. Cleanup removes only the final file. A two-file request without a valid CSRF token left the first file behind. The staff guard still applies; this is primarily a malicious/compromised staff-session disk-exhaustion path, not an anonymous upload bypass.

**Fix:** Accept exactly one file part, track/remove every temporary file on every exit, bound part counts and concurrent uploads, and validate CSRF before writing large bodies where the upload protocol allows it.

**Accept:** Duplicate-file, invalid-CSRF, oversized, canceled and failed-scan requests leave no staging files. The legitimate file-first upload flow remains supported or is deliberately updated on both client and server.

### A10 — Medium: filesystem symlinks escape the download root

**Where:** `internal/downloads/catalog.go:240,444`.

Path checks are lexical. Auto-discovery follows a `mods/leak.zip` symlink, marks its external target ready, and supplies that path to the file-serving handler. A synthetic file outside the root was readable through the catalog path. Exploitation requires creating/replacing a symlink in the downloads filesystem; the HTTP upload flow does not extract archives or itself create symlinks.

**Fix:** Use filesystem-enforced root confinement for reads/writes/deletes, including auto-discovered files and sidecars. Reject symlinks or open beneath a confined root without symlink escapes; a standalone pre-check is insufficient against replacement races.

**Accept:** Cataloged files, discovered files, parent-directory symlinks and replacement races cannot read or modify anything outside the downloads root.

### A11 — High if VPN peers can reach internal services: incomplete VPN isolation

**Where:** `internal/wg/agent.go:181`.

Generated forwarding allows selected TCP ports to **any destination**. It appends rules to `FORWARD`, so earlier host rules can accept traffic first, and it installs no `INPUT` policy protecting services on the VPN host. Client `AllowedIPs` is user-editable and does not enforce server-side isolation. The mocked-command test confirmed unrestricted destinations and absence of host-input rules; actual reachability was not tested.

**Fix:** Define explicit server-side destination/port rules for the realm, add a correctly ordered dedicated firewall policy, restrict host-local access from peers, and account for Docker DNAT and return traffic. Reconcile revoked peer files reliably: `wgDeletePOST` currently deletes the database row, ignores file-removal errors and announces success, while the agent continues trusting files. Bans also leave existing VPN peers provisioned; decide and implement whether suspension revokes VPN access.

**Accept:** In a disposable network namespace, peers reach only intended realm/site destinations; same-port LAN services and host management ports fail; pre-existing Docker rules do not bypass policy; failed peer deletion cannot falsely report completed revocation. This network-namespace validation remains follow-up work.

### A12 — Medium: reset tokens enter access logs

**Where:** `internal/web/middleware.go:34`.

The logger records the full URL path, including `/account/reset/{token}` and `/account/verify/{token}`. The synthetic request confirmed the reset token appears in the log. Someone with timely log access could reuse a live reset token; MFA still applies if correctly enabled.

**Fix:** Log route patterns/redacted paths for token-bearing routes, including error logging. Add `Cache-Control: no-store` to account, MFA, reset and other secret-bearing responses: `/account` renders game passwords and VPN QR data but currently lacks that header (VPN bundle responses already set it).

**Accept:** Captured success/error logs never contain fixture token values, and sensitive HTML/JSON responses have `no-store` without breaking static-asset caching.

### A13 — High with access to local files/build cache: secret files are not excluded from builds

**Where:** `.dockerignore`, `Dockerfile:7`, `.github/workflows/container.yml`.

`.gitignore` excludes root private keys and `data/wg`, but `.dockerignore` does not. `COPY . .` can therefore put locally present VPN keys into the builder layer. The workflow exports build cache with `mode=max`, which includes intermediate layers. This proves a build-context risk, not that these secrets reached GitHub: normal CI checkouts do not contain ignored local keys, and the final runtime stage copies only the binary/content. [Docker cache modes](https://docs.docker.com/build/cache/backends/).

Metadata-only inspection also found root `server.key`, `client1.key`, and `data/kb.sqlite` with mode **0644**; the data directory is **0755**. Other local users able to traverse the workspace can read them. SQLite holds sessions, TOTP secrets and VPN private keys, in addition to A01's recoverable game passwords. Key contents and directory-wide deployment state were not inspected.

**Fix:** Exclude all runtime data, keys and staging files from build contexts, preferably copy only required source directories. Provision private directories/files with restrictive ownership and modes, including SQLite WAL/SHM. Validate with synthetic key sentinels in an isolated build context. Investigate exposure before deciding which credentials/caches need rotation/removal; no rotation or deletion was performed here.

**Accept:** Synthetic keys are absent from every builder/cache layer; database and key files are inaccessible to unrelated local users while both app and VPN agent retain intended access.

### A14 — Medium, conditional: SMTP TLS setting can fall back to plaintext

**Where:** `internal/mail/mail.go:57`.

On implicit-TLS failure, the TLS-enabled path opens plain SMTP. If STARTTLS is absent and no SMTP auth is configured, it still sends the message. This can expose reset links on a network attacker-controlled or misconfigured relay path. With authenticated non-local SMTP, Go's `PlainAuth` may reject the insecure connection; this finding does not claim it always leaks SMTP credentials. Confirmed by control-flow review, not a live relay test.

**Fix:** Model implicit TLS and mandatory STARTTLS explicitly, fail if the required protection is unavailable, and set connection/operation deadlines.

**Accept:** A local fixture without STARTTLS never receives mail when TLS is required; valid implicit TLS and STARTTLS still work; stalled relays time out.

## Other hardening work and coverage limits

- **Proxy trust:** Public socket peers no longer get trusted XFF, but any private/link-local peer does, and the first header value is neither validated as an IP nor walked through a known proxy chain. Use explicit trusted proxy ranges and test tunnel, Docker and VPN ingress. Current production reachability is unknown.
- **Availability:** Every non-static/non-health request can create/persist an anonymous session before throttling. Long uploads/downloads have no read/write deadline; downloads have a request-rate limit but no active-transfer cap. Add bounded session issuance/storage, scoped deadlines and per-account/global work budgets. No flooding or resource-exhaustion test was performed.
- **CAPTCHA configuration:** Selecting a provider without both keys makes `Required()` false, so verification silently switches off. Fail startup for an incompletely configured enabled provider. This was statically reviewed; live CAPTCHA state was not checked.
- **Browser supply chain:** The armory loads unpinned upstream `viewer.min.js` through the same-origin proxy. CSP `script-src 'self'` therefore still permits upstream code to act with site privileges. Pin/review the viewer artifact or isolate it; no claim of malicious upstream content. jQuery 3.5.1 is vendored, but no dedicated JavaScript dependency scan was run.
- **SQL/SOAP:** Reviewed parameterized queries, database-name validation, ownership predicates and command rejection/escaping. Existing tests cover SRP6 and command builders; no direct SQL injection or SOAP command injection was established. SOAP/MySQL transport assumes trusted internal networking. No disposable MySQL integration fixture or complete AzerothCore installation was run, so grant application, fallback consistency and transactional cross-database failure behavior remain unverified.
- **Positive checks:** Existing player/ticket/character ownership, role gates, CSRF, raw-HTML escaping, lexical traversal, redirect and passkey origin tests passed. The passkey test covers normal register/login/delete; wrong-origin/challenge/concurrency coverage is not exhaustive. Armory inspect of other players is intentional, not an ownership defect.
- **Tooling limits:** Gitleaks, Trivy and Semgrep were not installed. The custom history scan is not an entropy scan and cannot establish absence of secrets. Dockerfiles/Compose/CI were reviewed manually; no OS-package container scan or synthetic build-context probe was run. No deployed service, firewall, DNS route or production image was assessed. These gaps are explicit follow-up checks, not clean bills of health.

## Recheck of the August 30 findings

The [previous assessment](security-assessment.md) remains historical. “Resolved” below means the cited code concern is addressed, not that production settings were verified.

| Old ID | Current disposition | Evidence / remaining work |
| --- | --- | --- |
| S0 — published MySQL | Deployment verification required | The database service belongs to external AzerothCore deployment; no current exposure evidence collected. |
| S1 — cookie/HSTS/origin | Partially resolved | HTTPS-derived secure default and boot warning exist; `.env.example` explicitly sets false, Compose still publishes origin. Verify actual deployment separately. |
| S2 — proxy trust | Partially resolved | Public peers ignored; private/link-local trust remains broad. See hardening work above. |
| S3 — published SOAP | Deployment verification required | External worldserver publish configuration was not assessed. |
| S4 — scanning disabled/exe | Partially resolved | Scanner wired; `.exe` removed; oversized files still skip scanning. A08. |
| S5 — unrestricted VPN | Partially resolved | Port rules added, destination/host/order gaps remain. A11. |
| S6 — GM1 full admin | Resolved in defaults | Admin default 3 and separate moderator ticket guard. Live override unknown. |
| S7 — TOTP in session DB | Partially resolved | TOTP/DEK/recovery fields excluded from JSON, setup read from pending enrollment; SQLite still mode 0644 locally. A13. |
| S8 — missing step-up | Resolved for stated flows | TOTP setup asks password; password change asks TOTP if enabled. Broader passkey enrollment and lookup failure gaps: A06/A07. |
| S9 — broad character UPDATE | Resolved in grant script | Column-level grant/revoke exists; actual grants not inspected or applied. |
| S10 — unlimited downloads | Partially resolved | Request limiter and logs added; concurrency/transfer budget still absent. |
| S11 — RP vs realm host | Resolved in configuration validation | RP must match site host; corresponding test passes. Multiple production hostnames remain deployment-specific. |
| S12 — upload name | Not substantiated as a security flaw | Filename policy/UX only. Unicode and dot-count limits remain. |
| S13 — pending ID rotation | Still present | Password-to-MFA transition retains ID. Address with A02. |
| S14 — Compose caps | Resolved for primary web service | `cap_drop: ALL` and `no-new-privileges` present. Demo differs; agent intentionally privileged. |
| S15 — bans bypassed | Partially resolved | Password/passkey checks and staff revocation added; pending MFA bypass and ignored lookup errors remain. A02/A07. |
| S16 — stale website rank | Partially resolved | Staff-level synchronization/current-role staff guard added; KB cached role and ignored persistence errors remain. A03. |
| S17 — TOTP start disables MFA | Partially resolved | Enabled-state guard stops sequential overwrite; check/upsert is not atomic against concurrent confirmation. A07. |
| S18 — password change/reset sessions | Partially resolved | Authenticated sessions revoked; pending sessions missed. A02. |
| S19 — verify GET mutation | Resolved for activation | GET peeks and renders confirmation; CSRF-protected POST consumes/creates. Token logging remains A12. |
| S20 — weak recovery/rate limits | Partially resolved | 80-bit codes, constant-time comparison, setup/confirm/disable throttled. Atomic consumption/replay and other verification endpoints remain A07. |

## Implementation order and completion criteria

1. **Credentials and secret storage:** A01/A13 together; design the versioned migration and reset behavior first. Add the no-password database-decryption regression test, then verify restrictive permissions and synthetic build exclusions.
2. **Authentication/authorization:** A02/A03/A04/A06/A07 together so revocation, synchronization and current-role checks use one coherent session design. Add tests before each fix and invert the applicable reproduction assertions afterward.
3. **Dependencies:** A05 in a dedicated upgrade change with full test/race results and the scanner delta; keep CI, local runner and Docker on the same supported Go series.
4. **Uploads/logs/mail:** A08/A09/A10/A12/A14, with small focused changes and failure-path tests.
5. **VPN/deployment:** A11 plus proxy/origin exposure checks in an isolated network fixture, followed by an operator-reviewed deployment change. Do not infer firewall effectiveness solely from generated command strings.

After fixes, run the full tests, race detector, targeted negative authorization cases and vulnerability scan. Monitor failed session revocations, failed security-state reads, rejected/quarantined uploads and VPN reconciliation failures using sanitized logs. This assessment is complete as a repository review with the limitations listed above; hardening changes and deployment verification have not been implemented.

Reproduction sources and instructions: [assessment artifacts](security-assessment-2026-09-06/README.md).

## Implementation update

A01/A13 and A02/A03/A04/A06/A07 have been addressed in the working tree. See [the hardening design, validation, and rollout instructions](security-hardening/README.md). The migration uses the existing stored derivation immediately, with independent versioned verification/wrapping keys and startup compaction, rather than waiting for each user's next password login. Selected reproductions now assert the fixed behavior; the original evidence remains historical. Other findings remain open. These changes have now been deployed on this host; the completed migration, health checks, and backup location are recorded in `security-hardening/deployment.json`.
