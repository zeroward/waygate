# NetBird and SSO migration plan

Replace built-in WireGuard management with externally managed NetBird connectivity. Add a single configurable OIDC provider, portal-first SSO signup, ownership-verified account linking, and SSO-only website authentication for linked users. Preserve WoW game credentials, account IDs, staff roles, and existing uncommitted work. Do not deploy or change running services.

## Agreed behavior

- Remove WireGuard packages, agent command, routes, UI, Compose/build targets, configuration and deployment synchronization. Stop creating its tables; retain existing private data for separate cleanup after backup.
- Offer NetBird instructions and an optional NETBIRD_SETUP_URL. Preserve customized KB articles.
- OIDC authorization code flow with PKCE S256, state, nonce, issuer/audience/signature/expiry validation. One provider and one identity per portal user; identify by issuer plus subject, never email matching.
- Unknown SSO identities can create a portal account or link an existing one. New accounts honor invite keys, captcha and rate limits, require verified email and a unique username, and create WoW credentials later.
- Linking requires fresh local password plus existing TOTP and fresh provider authentication. Atomically bind identity, migrate the saved-password key, remove local credentials and increment auth_version. Revoke old sessions; preserve ownership and privileges.
- SSO users cannot use local passwords, passkeys, TOTP, legacy claims or password recovery. No local fallback or unlink; recovery belongs to the provider. Local users retain current behavior.
- Rewrap per-user game-password keys under a private, versioned server key. Fresh same-identity SSO proof is required to reveal/create/change game credentials. Do not persist plaintext keys or tokens. Abort linking if migration fails.
- Add config, routes, identity/session types, migrations, provider setup instructions, backups and rollback documentation. Keep SSO disabled until configured.

## Implementation checklist

- [x] Save agreed plan before code changes.
- [x] Remove WireGuard and add NetBird guidance.
- [x] Implement OIDC and identity/session migrations.
- [x] Implement signup, linking and SSO-only enforcement.
- [x] Preserve encrypted game credentials with fresh SSO verification.
- [x] Validate migrations, authentication adversarial cases, UI and deployment behavior.
- [x] Run Go tests, race tests, vet, deployment tests and container build; record results.

## Acceptance tests

Test migration/repeat migration, rollback and concurrent linking; fake-provider login/signup/link/reauth; invalid claims/state/nonce/replay/redirects, cancellation and outage; every local bypass and stale session/challenge; local MFA/bans/staff/invites; encrypted credential preservation and missing keys; removed VPN routes and NetBird instructions; deployment without WireGuard.

## Delivery

Save plan → remove WireGuard → OIDC/schema → signup/linking/enforcement → credential migration → regression tests and runbook. Actual provider interoperability and production setup are deployment checks. Back up database and encryption key; retire only the old waygate-wg container before deployment. Never start an old binary against an upgraded database without restoring its matching backup.

## Validation results

Completed on 2026-10-01. No deployment or live configuration changes were performed.

| Check | Result |
| --- | --- |
| `go test ./...` | Passed on Go 1.26 |
| `go test -race ./...` | Passed in an offline Go 1.26 container using a source-only snapshot |
| `go vet ./...` | Passed |
| Deployment helper tests | All 7 passed |
| Container build | Passed using the repository Go 1.23 Alpine builder; image `waygate:sso-validation` |
| Container smoke check | Offline migration, HTTP health, account rendering, and rejection of the removed agent command passed |
| `git diff --check` | Passed |

The fake provider tests cover signature, issuer, audience/authorized party, nonce, expiry, PKCE, stale authentication, state replay, provider cancellation/outage, signup/invite/verified-email rules, explicit linking confirmation, local MFA during linking, encrypted credential preservation, action-specific reauthentication, old reset tokens, banned users, and local/pending-session rejection. Identity tests cover concurrent linking, rollback, repeated migrations, key rotation, and refusal to assign realm accounts to SSO users by username matching.

Final review added a same-origin continuation page for linking and reauthentication so cross-origin form redirects cannot be blocked by the existing `form-action 'self'` policy. The policy remains unchanged.

Next operational step: follow [SSO setup and migration](../sso-deployment.md). Configure and test the actual OIDC provider, back up the database/keyring, verify NetBird, retire the specific old VPN container, and deploy. These are intentionally deferred deployment tasks. Real-provider interoperability and a graphical-browser check remain part of that cutover.
