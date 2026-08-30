# Gatehouse

Realm portal for **AzerothCore Wrath of the Lich King 3.3.5a**. Source lives in [`zeroward/waygate`](https://github.com/zeroward/waygate); container images are `ghcr.io/zeroward/waygate`.

Go 1.23, server-rendered HTML, MySQL 8, optional SOAP. One static binary. Designed to sit on the same Docker network as `ac-database` and `ac-worldserver` (Fedora CoreOS friendly).

This is not a PHP port of WoWSimpleRegistration. The two things that actually break AzerothCore websites are handled on purpose:

1. **Account create must produce SRP6 `salt` + `verifier`**, not the old `sha_pass_hash`.
2. **Online counts must exclude random playerbots** (`rndbot*` by default) or the status page lies.

## Architecture

```
browser  →  Gatehouse (waygate) :3080
               │
               ├─ MySQL  ac-database:3306
               │     acore_auth.account / account_access / realmlist
               │     acore_characters.characters
               │     acore_world.version          (optional)
               │
               └─ SOAP   ac-worldserver:7878      (docker network only)
                     account create / set password / set addon / set email
```

| Piece | Role |
| --- | --- |
| `acore_auth.account` | Username (uppercase), `salt` BINARY(32), `verifier` BINARY(32), email, expansion |
| `acore_auth.account_access` | Hide GMs when `HIDE_GM=true` |
| `acore_auth.realmlist` | Unused for public address — **public host/port come from env** so this host can publish world as **28085**, not 8085 |
| `acore_characters.characters` | Online list, leaderboards, account character list; **UPDATE** for unstuck (hearth/homebind) |
| `acore_characters.character_homebind` | Inn bind used by Account → Unstuck |
| SOAP `executeCommand` | Preferred write path so the **core** generates SRP6 data |

Website login verifies the password with the same SRP6 verifier the authserver uses. Website sessions are **not** in-game sessions.

### SOAP vs SQL SRP6

`ACCOUNT_CREATE_MODE`:

| Value | Behaviour |
| --- | --- |
| `soap` | SOAP only. Core writes salt + verifier. |
| `sql` | Direct parameterized `INSERT`/`UPDATE` with this app’s SRP6 implementation (AzerothCore algorithm). |
| `auto` (default) | SOAP when configured; fall back to SQL SRP6 if SOAP is down. Duplicate username is never inserted twice. |

SOAP commands (quoted, XML-escaped; `"` in passwords is rejected):

```
account create "USER" "PASS" "email@host"
account set password "USER" "NEW" "NEW"
account set addon "USER" 2
account set email "USER" "email" "email"
```

The SOAP account must be **gmlevel 3**, `RealmID = -1`. Keep it off the public internet. In `worldserver.conf`:

```
SOAP.Enabled = 1
SOAP.IP = "0.0.0.0"
SOAP.Port = 7878
```

`127.0.0.1` will not accept connections from the `waygate` container.

SQL fallback (AzerothCore wiki):

```
h1 = SHA1(UPPER(username) + ":" + UPPER(password))
h2 = SHA1(salt || h1)          # binary concat
v  = g^h2 mod N                # little-endian, 32 bytes
g  = 7
N  = 0x894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7
```

`sha_pass_hash` is **not** written. Modern AzerothCore dropped it.

### Playerbots inflating “online”

`BOT_USERNAME_PREFIXES` (default `rndbot`) is matched case-insensitively against `account.username`. AzerothCore stores names uppercase, so `RNDBOT%` is filtered from:

- home online count
- `/online`
- `/leaderboards`

`HIDE_GM=true` drops `account_access.gmlevel > 0` and `characters.extra_flags & 1` (GM ON) from **leaderboards**. Default is `false` so staff who also play still rank. The online roster already includes GMs; bots stay filtered.

The home page lists **installed AzerothCore modules** by scanning `MODULES_DIR` (the same `modules/` tree worldserver compiles). Compose bind-mounts `AC_MODULES_DIR` from the host. Names also merge with `acore_world.module_string` when that table exists.

**Downloads** (including file URLs) and the **Knowledge Base** (`/kb`) require a logged-in account of any level. Anonymous requests redirect to login. `/connect` redirects to `/kb/how-to-connect`. Editing requires GM 3+.

Logged-in accounts with `account_access.gmlevel` ≥ `GM_MIN_LEVEL` get an **Admin panel**: list registrations (bots hidden by default), create player accounts, reset passwords via SOAP/SRP6, upload or remove **Downloads**, and set rank to **GM** (2) or **Admin** (3). Knowledge Base create/edit is **Admin (GM 3+) only**. Super GM (4 / console) cannot be granted from the site. You can only assign a rank below your own, you cannot change your own rank, and you cannot modify an account whose GM level is higher than yours.

## Quick start (offline UI)

No WoW databases required:

```bash
cp .env.example .env
docker compose -f docker-compose.demo.yml up --build
# or: docker-compose -f docker-compose.demo.yml up --build
# http://127.0.0.1:3080
```

Demo mode uses in-memory accounts and a fake Icecrown roster so you can develop the UI.

## Downloads (client zip, patches, mods)

The **Downloads** tab lists files from `downloads/` on the host:

| Path | What goes there |
| --- | --- |
| `downloads/catalog.json` | Titles, descriptions, versions, optional SHA-256 |
| `downloads/client/` | The 3.3.5a `.zip` (e.g. `WoW-3.3.5a.zip`) |
| `downloads/patches/` | Optional patches |
| `downloads/mods/` | Optional mods / addons |

Admins can upload files from the **Admin panel** (Downloads section). That writes the archive into `client/`, `patches/`, or `mods/` and updates `catalog.json`. Compose runs **ClamAV** (`clamav:3310`); uploads are streamed to `clamd` (INSTREAM) and rejected if infected or if the scanner is down. Dropping a zip on the host still works (auto-scan within a few seconds) and is **not** scanned. Catalog entries whose files are missing appear as “not uploaded yet.” Large archives stay on the host — they are gitignored and bind-mounted **read-write**. The compose file runs the process as `WEBREG_UID`/`WEBREG_GID` (default 1000) so it can write `./downloads`. ClamAV wants about 3–4 GiB RAM. See `downloads/README.md`.

Download URLs are `/downloads/{id}` (catalog `id`), never raw filesystem paths.

## Run next to AzerothCore

1. Enable SOAP as above.
2. Create a SOAP GM (worldserver console, **not** this website):

   ```
   account create soapuser 'a-long-secret'
   account set gmlevel soapuser 3 -1
   ```

3. Create the MySQL user in `docs/mysql-grants.sql`.
4. Copy `.env.example` → `.env`. Set `DEMO_MODE=false`, MySQL, SOAP, `PUBLIC_HOST`, `PUBLIC_WORLD_PORT=28085` (or whatever you publish).
5. Point `AC_NETWORK` at the **real** Compose network name. AzerothCore’s file declares `ac-network`, but Docker Compose prefixes the project directory, so the network is usually `azerothcore-wotlk_ac-network`, not `ac-network`:

   ```bash
   docker network ls | grep ac-network
   # example: azerothcore-wotlk_ac-network
   ```

   Set that value in `.env` (`AC_NETWORK=…`), then:

   ```bash
   docker compose up -d --build   # or docker-compose up -d --build
   ```

   Host port **3080** maps to the `waygate` container. The PHP `ac-webreg` container on this host is left alone. Image: `ghcr.io/zeroward/waygate`.

6. Put a reverse proxy in front for HTTPS, then set `SESSION_SECURE_COOKIE=true` and `SITE_URL=https://…`.

7. **Player VPN (WireGuard)** is optional. It runs as the compose `wireguard` service (`network_mode: host`, UDP **51820**) and **replaces** host `wg-quick@wg0`:

   ```bash
   sudo systemctl disable --now wg-quick@wg0
   # leave /etc/wireguard/wg0.conf as a backup
   ```

   Then set `WG_ENABLED=true` in `.env` and `docker compose up -d --build`. Registered users mint up to 5 device configs on Account (QR + zip bundle). Split tunnel only — not a general internet exit. Recreate any old `10.10.10.x` peer from the website (`10.8.0.0/24`). Do not start `wg-quick@wg0` again.

## Environment

See `.env.example` for the full list. Important variables:

| Variable | Purpose |
| --- | --- |
| `AC_NETWORK` | Existing Docker network to join (Compose name, e.g. `azerothcore-wotlk_ac-network`) |
| `WEBREG_PORT` | Host port published by compose (default 3080) |
| `DEMO_MODE` | Skip MySQL/SOAP; fake status |
| `LISTEN_ADDR` | Bind address (`:3080`) |
| `SITE_URL` | Public origin for mail links and passkeys (RP ID is the hostname) |
| `WEBAUTHN_RP_ID` / `WEBAUTHN_ORIGINS` | Optional passkey overrides; default from `SITE_URL` |
| `WG_ENABLED` | Show Account VPN panel; requires the compose `wireguard` service |
| `WG_ENDPOINT` / `WG_PORT` / `WG_PEER_MAX` | Default client endpoint (admins can override on the Admin panel), listen port, 5 configs per user |
| `REALM_NAME` / `SITE_BLURB` / `CORE_NAME` | Home page |
| `PUBLIC_HOST` | `realmlist.wtf` hostname |
| `PUBLIC_AUTH_PORT` | Usually 3724 |
| `PUBLIC_WORLD_PORT` | Public world port (**28085** on this host, not 8085) |
| `MYSQL_*` / `AUTH_DB` / `CHARACTERS_DB` / `WORLD_DB` | AC databases |
| `WORLD_HOST` / `WORLD_PORT` | Internal probe (`ac-worldserver:8085`) |
| `SOAP_*` | Internal SOAP (`ac-worldserver:7878`) |
| `ACCOUNT_CREATE_MODE` | `auto` / `soap` / `sql` |
| `BOT_USERNAME_PREFIXES` | Comma list, default `rndbot` |
| `HIDE_GM` | Hide GM characters from leaderboards (default `false`) |
| `GM_MIN_LEVEL` | Minimum `account_access.gmlevel` for the Admin panel at `/staff` (default 1) |
| `STATUS_CACHE_SECONDS` | 15–30 recommended |
| `CAPTCHA_PROVIDER` | `none` / `turnstile` / `hcaptcha` |
| `SMTP_*` | Enables contact form + 15‑minute single-use password reset |
| `DISCORD_URL` / `CONTACT_EMAIL` | Shown when SMTP is unset |
| `TRUST_PROXY` | Honour `X-Forwarded-For` only behind a known proxy |
| `HOW_TO_CONNECT_FILE` | Markdown, placeholders `{{PUBLIC_HOST}}` etc. |
| `DOWNLOADS_DIR` | Host folder for client/patches/mods (default `downloads`) |
| `DOWNLOADS_CATALOG` | JSON catalog path (default `{DOWNLOADS_DIR}/catalog.json`) |
| `AC_MODULES_DIR` | Host path to AzerothCore `modules/` (bind-mounted into the container) |
| `MODULES_DIR` | Directory scanned for installed mods (default `/azerothcore/modules` in Docker) |

Never commit `.env`. SOAP passwords and MySQL passwords stay server-side.

## Security

- Parameterized SQL only. Database names from env are restricted to `[A-Za-z0-9_]`.
- Passwords are never logged. SOAP faults are mapped to generic client errors.
- Helmet-style headers, 64 KiB form limit, CSRF, httpOnly session cookie (`Secure`, `SameSite=Lax`).
- Session ID: 32 random bytes, server-side memory store (v1). **TODO:** Redis-backed sessions for multi-replica.
- Rate limits on register, login, contact, reset.
- Captcha on register when configured. **Production should not use `CAPTCHA_PROVIDER=none`.**
- **TODO:** vote-for-points (not in v1).
- Website login supports TOTP and passkeys. Neither applies to the 3.3.5a client.
- Email uniqueness is enforced with a `SELECT` then `INSERT`. AzerothCore has no unique index on `account.email`. Do not treat that as a security boundary.

Versioning is SemVer. This tree is **0.x alpha** (`v0.1.0-alpha.1`) until it has had in-depth security testing. Pre-releases are GitHub prereleases; `latest` on GHCR tracks `main`, not alphas.

## Container

CI on `main` builds and pushes `ghcr.io/zeroward/waygate` (also `latest` and `sha-*`). Pull requests run tests and a docker build without pushing.

```bash
docker pull ghcr.io/zeroward/waygate:latest
```

The GitHub Actions token needs `packages: write` (this workflow sets that). The GHCR package should be linked to this repository.

## Tests

```bash
make test
# or: docker run --rm -v "$PWD":/src -w /src golang:1.23-alpine go test ./...
```

Covers username validation, the sliding-window rate limiter, a known SRP6 verifier vector, SOAP command XML escaping, and the demo register flow.

## Layout

```
cmd/waygate/         process (Gatehouse)
internal/srp6/       AzerothCore SRP6
internal/soap/       executeCommand client
internal/account/    SOAP + SQL + demo memory
internal/status/     cached online / boards, bot + GM filters
internal/web/        HTML pages, Icecrown CSS
content/             seed source for how-to-connect (mounted read-only)
data/                SQLite knowledge base (kb.sqlite)
docs/mysql-grants.sql
```

## License

Use as you wish on a private realm. World of Warcraft is a trademark of Blizzard Entertainment. This project is not affiliated with Blizzard or the AzerothCore project.
