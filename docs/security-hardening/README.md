> Historical hardening notes. The WireGuard runtime has since been removed. For current authentication and deployment procedures, see [SSO setup and migration](../sso-deployment.md).

# Credential and authentication hardening

Implements assessment A01/A13 and A02/A03/A04/A06/A07. This change is implemented, tested, and deployed on this host. See `deployment.json` for the verified deployment and backup location. The other assessment findings remain open.

## Credential format and migration

New password records use `waygate-argon2id-v2`. Argon2id retains the existing parameters (64 MiB, one iteration, four lanes, 16-byte random salt). HKDF-SHA256 derives independent 32-byte password-verification and DEK-wrapping values with separate purpose labels. Only the verification value is stored. AES-GCM wrapping authenticates the format version and user ID as associated data.

Startup converts existing Argon2 records using the legacy stored derivation, rewraps the existing DEK, and converts pending signup hashes in one transaction. No password prompt or password change is required. The old raw derivation is removed from active records. Malformed or unknown password formats stop startup. An unreadable legacy wrap clears only the affected saved game-password copies, retaining their account links. Empty legacy-account markers remain eligible for first-time claiming; a game login cannot overwrite an established website password.

The `identity_meta.security_v2` marker advances through `compacting` to `complete`. Startup checkpoints the WAL, vacuums SQLite, and checkpoints again before accepting traffic. Interrupted compaction retries on restart. Completed migrations do not run again. Initial migration removes all old HTTP sessions and increments authentication versions, signing everyone out once.

Normal password changes atomically replace the verifier and wrapped key while preserving saved game passwords. They require the old password and, when enabled, a fresh authenticator or recovery code. Email resets clear the wrap and saved game-password copies, preserving linked game accounts, actual game passwords, enrolled MFA, and passkeys. The account page lets users verify and save their current game password again. Pending authenticator enrollment is invalidated by either operation.

Password replacement compares both the previous hash and authentication version. Initial DEK creation uses a conditional write, so concurrent unlocks obtain the same key. Credential-copy writes and credential-management transactions reject stale authentication versions.

## Sessions and account verification

Session handles expose detached snapshots and synchronized, explicit updates. There is no deferred response-end save. Revoked rows cannot be recreated by an update or stale rotation. Rotation replaces both the session ID and CSRF token; the replacement is bound only to the request performing the rotation. Old in-flight requests cannot follow another request into its authenticated session. Decryption keys and one-time recovery-code displays are never serialized into the session database.

Pending MFA and authenticated sessions share the durable user index and authentication-version checks. Pending login lasts five minutes and rotates again after factor verification. Every non-static request with an authenticated or pending session checks the current user version and ban state. Staff and KB guards reload current website roles. Password, MFA, passkey, and role changes increment the version; successful self-service changes rotate the current session and revoke others. Database lookup failures fail closed.

Adding/removing passkeys and starting/disabling TOTP require fresh password verification plus MFA when enabled, or an existing passkey with user verification. Grants last five minutes, are bound to one action, and are consumed once. WebAuthn challenges are also purpose-bound, expire after five minutes, and are consumed before validation. Passkey enrollment and login require the authenticator's user-verified flag. Anonymous login challenges predating a credential change are rejected.

TOTP setup lives separately from active MFA and expires after ten minutes. An active factor cannot be overwritten by starting enrollment. Confirmation, timestep consumption, and recovery-code consumption are transactional; replay and write failures are rejected. Password checks, unlock, MFA, and passkey authentication use account and IP attempt limits where the account is known. Before discovery, passkey requests use the shared IP limit.

Website role changes commit and revoke sessions before game-server synchronization. Partial external synchronization is reported as incomplete so it can be retried. Ban operations revoke existing sessions before synchronizing linked game accounts; current game ban state is checked on subsequent requests.

Run **one active web application instance per SQLite database**. Process-local session caching and rate limits are not a distributed multi-instance design. Multiple browser requests within that instance are supported and race-tested.

## Private files and Docker

Application startup uses a restrictive umask. Dedicated SQLite and WireGuard directories are `0700`; databases, existing WAL/SHM files, environment files, keys, peer files, generated configuration, and health files are `0600`. Permission errors fail startup/storage operations. Existing symlinks in private storage are rejected. `.env` load failures are no longer silently ignored.

The root WireGuard agent uses configurable `WG_DATA_UID` / `WG_DATA_GID`. Compose maps these to `WEBREG_UID` / `WEBREG_GID`; direct execution defaults to the process owner. The old hardcoded owner and world-readable directory changes are removed.

Docker copies explicit source directories and uses an allowlist build context with additional secret-file exclusions. Runtime data, `.env`, SQLite files, private keys, archives, and repository metadata are excluded before builder layers are created. The dependency graph is built with `-mod=readonly`.

## Rollout

The supported local Compose workflow is now `make deploy-check` followed by `make deploy` (or `python3 scripts/deploy.py`). The deployment helper performs the offline backup, permission repair, migration, startup, and health checks below. Backups and previous image IDs are retained under `.deployment-backups/`; the helper never automatically restores an older database. Run with the services' data owner, or root if the ownership preflight requires it. Stop any additional database clients first. `waygate migrate --db /app/data/kb.sqlite` is also available for controlled offline maintenance; it does not open MySQL connections or an HTTP listener.


1. Stop the web service, WireGuard agent, and any other clients of this SQLite database. Keep public traffic stopped for the migration. Make a consistent offline backup of the database and private data, keeping the backup private. Historical backups and previously built images/cache can still contain the old decryption material; migrating the active database cannot erase those copies.
2. Match the configured website UID/GID and WireGuard data owner. Check the dedicated data directory and `.env` before starting either service. For example, replacing these sample paths and IDs with the deployment's values:

   ```sh
   python3 docs/security-hardening/permissions.py --root /srv/waygate --data /srv/waygate/data --uid 1000 --gid 1000
   ```

   With the services stopped, add `--apply` to repair modes and ownership. Ownership changes require the appropriate privileges. If `KB_PATH` or `WG_DIR` lives outside this data directory, check/repair its dedicated directory separately. Do not point this tool at a shared system directory.
3. Deploy the new image and start one web instance. Startup must finish migration and compaction successfully before serving requests. An invalid-format or permission error requires correction while offline; do not bypass migration. Allow free disk space for SQLite compaction and the backup.
4. Start the agent with the matching data owner. Verify health, password login, password-plus-MFA login, PIN/biometric passkey login, saved credential unlock, and role changes. Users should expect one deployment sign-out. Restarting after a completed migration should not cause another global sign-out.
5. Prefer rolling forward. The old binary cannot read the new password format. Recovery using an offline backup requires restoring a coherent application/database version and invalidating restored sessions before reopening traffic; intervening changes would be lost. Do not run the old and new binaries against the same database.

## Verification

Run normal and race tests with Go 1.23 or the repository's supported newer toolchain; race mode needs a C compiler:

```sh
go test ./...
go vet ./...
go test -race ./...
python3 docs/security-assessment-2026-09-06/run_reproductions.py
python3 docs/security-hardening/check_build.py
```

`check_build.py` creates a temporary source-only context and fake secret sentinels. It builds a context-inspection target, the real builder target, and the website runtime target, then inspects every saved layer, including compressed OCI layers. This checks material retained by builder COPY layers as well as the final filesystem. It does not inspect or delete historical production caches. Unique test images and temporary archives are retained for review.

Evidence in this directory records normal tests, vet, race tests, updated reproductions, synthetic permissions, and build exclusions. Initial red tests reproduced database-only credential decryption and permissive SQLite modes; additional red tests caught cross-request session adoption and the legacy game-login overwrite. The corresponding regressions now pass. The selected original reproductions have been inverted; unaddressed findings retain their baseline assertions and are not claimed fixed.
