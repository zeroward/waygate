# NetBird and SSO setup

Gatehouse now uses externally managed NetBird connectivity and supports one OpenID Connect provider. Existing users remain local until they explicitly link SSO. New SSO users create a portal account first and add WoW client credentials afterward. This change does not configure or deploy a provider or modify running services.

The implementation checklist and validation record are in [the migration plan](plans/netbird-sso-migration.md).

## Provider configuration

Create a confidential web application in your OIDC provider:

- Use authorization code flow with PKCE S256 and client authentication. Enable the `openid`, `profile`, and `email` scopes.
- Register exactly `https://YOUR-PORTAL-HOST/auth/oidc/callback` as the redirect URI. This is the `SITE_URL` origin, not the game hostname.
- Enable verified email claims for new signup. Linking an existing account requires its local credentials, regardless of email claims; matching emails never links accounts automatically.
- Support `prompt=login`, `max_age=0`, and a signed `auth_time` claim for linking and credential actions. Keep provider and portal clocks synchronized. Gatehouse allows 30 seconds of clock skew.
- Configure MFA, provider account recovery, and who may use this application at the identity provider. Gatehouse does not apply its own TOTP challenge to SSO users.

Set these values in the private deployment environment:

```dotenv
SITE_URL=https://YOUR-PORTAL-HOST
SESSION_SECURE_COOKIE=true
SSO_ENABLED=true
OIDC_ISSUER=https://YOUR-PROVIDER/EXACT-ISSUER-PATH
OIDC_CLIENT_ID=YOUR-CLIENT-ID
OIDC_CLIENT_SECRET=YOUR-CLIENT-SECRET
OIDC_DISPLAY_NAME=Your identity provider
SSO_VAULT_KEY_FILE=data/sso-keys.json
NETBIRD_SETUP_URL=https://YOUR-NETBIRD-ENROLLMENT-OR-INSTRUCTIONS-PAGE
```

The issuer must exactly match provider discovery and signed tokens, including any trailing slash. `SITE_URL` must be an origin, without a subpath. HTTPS is required; loopback HTTP is allowed only with `DEMO_MODE=true`. Leave `SSO_ENABLED=false` until configuration and the keyring are ready. The NetBird link is optional.

Provider discovery is lazy and retries after failure. Existing local accounts continue using their current authentication. Provider outages and disabling SSO never re-enable local access for linked accounts. Local logout terminates the Gatehouse session only; it does not sign out of the identity provider or NetBird. Provider account deactivation is checked on subsequent authentication, not continuously during an existing portal session; Gatehouse bans are checked on every authenticated request. Portal sessions retain the configured `SESSION_TTL_HOURS` lifetime.

## Private encryption keyring

SSO cannot derive a saved-password encryption key from a website password. On linking, Gatehouse rewraps the user's existing key with a private server key. Game passwords remain encrypted in SQLite. The keyring belongs outside the database and source control; the default `data/` directory is excluded from Git and image builds.

Generate a keyring once, on the deployment host, before enabling SSO. This example refuses to overwrite an existing file:

```bash
python3 - <<'PY'
import base64, json, os, secrets
from pathlib import Path
path = Path('data/sso-keys.json')
path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    json.dump({'active': 'v1', 'keys': {
        'v1': base64.b64encode(secrets.token_bytes(32)).decode()
    }}, f)
    f.write('\n')
    f.flush()
    os.fsync(f.fileno())
PY
```

The application UID must be able to read the file. It must be a regular file with no group/other permissions; do not use a symlink. The existing Compose data mount makes the default path available inside `/app/data`. The deployment helper repairs ownership to `WEBREG_UID:WEBREG_GID` and includes this path in its private data backup. If you choose an external path, mount it read-only into the container and back it up separately before deployment.

Back up the database and keyring together in private storage. Database copies without the keyring cannot decrypt SSO game credentials. Gatehouse never generates a replacement key on restart and refuses startup if the configured keyring cannot decrypt existing SSO wrappers. This check also runs when SSO is disabled but linked accounts exist.

For rotation, add a new independently generated 32-byte base64 key under a new ID, change `active` to that ID, retain every previous key, and restart the application. Successful game-credential verification rewraps that user's key under the active key. Retain old keys while any `users.sso_dek_key_id` references them and for as long as retained database backups require them. Replacing the contents under an existing key ID is not rotation and will make those wrappers unreadable.

## Account behavior

**Existing users:** Account → Connect single sign-on requires the current website password and existing authenticator/recovery code, then fresh provider authentication and an explicit confirmation. The confirmation preserves the portal ID, WoW links, staff rank, tickets, and saved game passwords. It removes the website password hash, passkeys, TOTP secret, and recovery codes; increments the authentication version; and revokes prior sessions. A failed link leaves the local account usable. Linking expires after five minutes and must restart if the application restarts before confirmation.

**New users:** Sign in with SSO → Create a new account requires a unique portal username, verified provider email, the configured registration key, and the existing captcha/rate-limit checks. No WoW account or staff privilege is granted at signup. Existing realm usernames are reserved for their owners. Create WoW credentials from Account after fresh SSO verification.

**Saved game passwords:** Separate verification buttons authorize viewing passwords, adding a client login, or changing/saving a password. Viewing expires after five minutes; mutation grants are single-use and expire after five minutes. The provider identity must exactly match the linked issuer and subject. Decrypted keys are kept only in memory and are not restored with persistent sessions. Staff game-password resets remain game operations and cannot restore website password login.

**Recovery:** There is no unlink button, local fallback, or operator relinking command. Recover the same identity through the provider. Changing the provider issuer or a user's subject does not migrate their binding. Plan any provider migration separately; do not manually edit binding rows to bypass ownership checks.

## Deployment and WireGuard retirement

The code update removes WireGuard management, routes, configuration, Docker stage, and Compose service. It does not delete existing `wg_peers` rows, VPN files, keys, or historical assessment artifacts. Removing a Compose service alone does not stop its old container.

Before first deployment:

1. Validate NetBird connectivity independently and set `PUBLIC_HOST` to the game hostname reachable over that network. Customize the setup link and player guidance. Existing knowledge-base articles are preserved and may need an administrator's content update.
2. Back up the existing database, environment, Compose configuration, and private VPN data. Identify the old `waygate-wg` container and retire that specific container with `docker stop waygate-wg` followed by `docker rm waygate-wg`. Do not run broad orphan removal; keep NetBird, Pangolin, and other tunnel services running. Verify any obsolete interface/firewall cleanup against the old VPN configuration before removing it.
3. Remove obsolete `WG_*` environment settings when editing deployment configuration; they are now ignored. Create and back up the SSO keyring, then configure the provider and environment.
4. Run `make deploy-check`, then `make deploy`. The helper builds before downtime, stops the website, backs up private data, runs the offline SQLite migration, and restarts the website with a health check. No WireGuard synchronization is performed.
5. Test the real provider with a non-staff test account: local login, link confirmation, SSO-only login, logout, provider recovery, game-password reveal/create/change, and invite-controlled SSO signup. Confirm a previously issued reset link and an old session cannot access the linked account. Test provider reauthentication on the browsers you support.
6. Verify staff access and realm login through NetBird before migrating administrator accounts. Remove unused legacy VPN data separately only after confirming backups and successful cutover.

On migration/startup failure, keep the failed application stopped and retain the backup. Correct provider/key configuration and retry. For a binary rollback, restore the corresponding pre-upgrade database and private files together; never start an old binary against a database containing SSO accounts. Restoring a backup discards account changes made afterward.

## Validation

Automated tests use a local signed-token OIDC provider and disposable databases. They cover signup, ownership verification, MFA during linking, credential preservation, local-path rejection, stale sessions/reset links, token/state/nonce/PKCE failures, reauthentication purpose/expiry, provider failures, key rotation, migration idempotence, and concurrent linking. Production provider interoperability and NetBird connectivity must still be checked during deployment.
