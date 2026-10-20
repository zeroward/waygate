# Management API v1

The read-only management API runs on the same listener as Gatehouse. It exposes realm dashboard data to a server-side integration. It does not expose offline character directories, private account records, website sessions, historical metrics, or administrative actions.

## Enable and connect

Generate a dedicated secret using `openssl rand -hex 32`, then set `MANAGEMENT_API_TOKEN` in Waygate's environment (or its private `.env` file). The token must contain at least 32 printable ASCII characters without spaces. Empty disables the entire `/api` namespace with JSON 404 responses. A malformed configured token prevents startup.

Restart Waygate after changing the token. With Compose, recreate the service so the new environment is loaded; a plain container restart retains its old environment. The production Compose file loads `.env`; the demo Compose file also accepts `MANAGEMENT_API_TOKEN` from the environment or Compose `.env`. Demo responses contain `demo: true` and synthetic data.

Keep the token in your management backend's secret configuration. Use HTTPS or a trusted private network, and ensure any reverse proxy forwards the `Authorization` header. The API does not accept tokens in query strings or cookies and does not enable browser CORS access. Any upstream access gateway still needs to allow the backend's connection.

With `WAYGATE_URL` set to the portal origin and `MANAGEMENT_API_TOKEN` supplied securely to your shell:

```bash
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer ${MANAGEMENT_API_TOKEN}" \
  "${WAYGATE_URL}/api/v1/dashboard"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer ${MANAGEMENT_API_TOKEN}" \
  "${WAYGATE_URL}/api/v1/players"
```

One token is supported. To rotate, generate a replacement, update both services' secret configurations, and recreate/restart Waygate and reload the management backend. The old token stops working when Waygate loads the replacement. Setting an empty token and restarting disables access. No database migration is needed. API credentials do not authorize website staff routes.

## Resources and responses

The [OpenAPI contract](management-api.openapi.json) specifies every response field. Paths are exact, with no trailing slash. No filtering or pagination parameters are supported in v1; query parameters are ignored. All resources also support HEAD with the same status and headers and no response body. Other methods return 405 with `Allow: GET, HEAD` after authentication.

| GET path | `data` |
| --- | --- |
| `/api/v1/dashboard` | Object containing `status`, `players`, `leaderboards`, and `modules` |
| `/api/v1/status` | Realm identity, reachability, core version, configured public host/ports, and population |
| `/api/v1/players` | Online character array: name, level, race/class names and IDs, zone, faction |
| `/api/v1/leaderboards` | Arrays named `playtime`, `kills`, `honor`, `arena`, and `gold` |
| `/api/v1/modules` | Array of detected module IDs and titles |

Successful responses have this envelope (an empty online roster is shown):

```json
{
  "generated_at": "2026-10-01T12:00:00Z",
  "cache_ttl_seconds": 20,
  "demo": false,
  "partial": false,
  "data": [],
  "errors": []
}
```

Timestamps use RFC 3339 UTC, optionally with fractional seconds. `generated_at` identifies when collection began. The API shares the website's status snapshot and `STATUS_CACHE_SECONDS` setting (default 20, minimum 5). Collection runs on demand, has a ten-second budget, and coalesces concurrent requests. The cache lifetime starts after collection finishes. Failed collections are cached for the same interval and retried on the next request after expiration; no older successful values are substituted. Client disconnects do not cancel collection for other callers.

Poll the aggregate dashboard every 20 seconds by default, or at the reported cache interval. Responses include `Cache-Control: no-store`; this prevents intermediary/browser caching while Waygate retains its own collection cache. Separate calls can straddle a refresh, so use the dashboard when all sections should come from one snapshot. Collection performs sequential reads, not a transactional snapshot of live game activity.

## Meaning of the data

- `realm_up` means the existing `server info` SOAP probe succeeded, or a fallback TCP connection to the configured worldserver succeeded. It is not an authserver test or a guarantee of gameplay readiness. A failed reachability probe is a valid `realm_up: false` response when the remaining status data can be collected.
- `population.humans` includes GMs and excludes accounts matching `BOT_USERNAME_PREFIXES`. `gms` is a subset of humans; `total = humans + bots`. Alliance and Horde counts cover humans. Deleted characters are excluded. Counts refer to online characters, not unique website users.
- The online roster includes GMs regardless of `HIDE_GM`, excludes bot accounts, and also excludes the existing `AUCTIONEER` character-name denylist. It is ordered by level descending, then name ascending. Because aggregate counts do not apply this name denylist, roster length may differ from `population.humans`. Demo data is illustrative, not a production count invariant.
- Leaderboards include offline characters, exclude bots and the character-name denylist, and honor `HIDE_GM`. Each board is limited by `LEADERBOARD_SIZE` (default 20, maximum 100). `raw` is numeric; `value` is display text. Raw units: playtime **seconds**, kills **count**, honor/arena **points**, gold **copper** (10,000 copper = 1 gold). Arena points are not an arena rating.
- Modules are detected from `MODULES_DIR` and optional world `module_string` rows, merged by ID. This is discovery, not runtime health verification. An unconfigured source, a missing optional world table (MySQL error 1146), or an empty version table is treated as absent data. `world_version` is an empty string when absent. An unreadable configured module directory or a real database error is reported as unavailable. Optional module README/metadata files remain best-effort.

## Failures

The dashboard returns HTTP 200 even if collection is incomplete. Check `partial` and `errors` before using its data. Each unavailable section is `null`; healthy empty lists are `[]`. A failure in any leaderboard makes the entire `leaderboards` section unavailable. A failure collecting status counts or a configured world version makes the `status` section unavailable.

For example, when only the roster query fails, the dashboard includes:

```json
{
  "section": "players",
  "code": "players_unavailable",
  "message": "Requested data could not be collected."
}
```

This object appears in `errors`, with `data.players: null` and `partial: true`. The other sections remain usable. If all sections fail, all four are null and all four errors are listed.

A dedicated endpoint whose section cannot be collected returns HTTP 503, with no success envelope:

```json
{"error":{"code":"players_unavailable","message":"Requested data could not be collected."}}
```

| HTTP | Error code | Meaning |
| --- | --- | --- |
| 401 | `unauthorized` | Missing/invalid bearer token, with a `WWW-Authenticate` challenge |
| 404 | `not_found` | API disabled, or an unknown path/version after authentication |
| 405 | `method_not_allowed` | Authenticated request used a method other than GET/HEAD |
| 503 | `status_unavailable`, `players_unavailable`, `leaderboards_unavailable`, `modules_unavailable` | Dedicated resource could not be collected |
| 500 | `internal_error` | Unexpected request failure |

Authentication runs before route and method checks when enabled. Error bodies never include SQL, internal hosts, credential values, or stack traces. API calls do not create/refresh website sessions or write application data; the only SOAP command used by collection is the existing read-only `server info` probe.

---

# Programmatic Administration API (`Admin API v1`)

The programmatic administration API lives under `/api/v1/admin/...` on the same listener as Gatehouse. It is authenticated by a dedicated `ADMIN_API_TOKEN` bearer secret and is completely separate from `MANAGEMENT_API_TOKEN`.

## Authentication, Scope, and Actor Attribution

1. Generate a dedicated secret using `openssl rand -hex 32` and set `ADMIN_API_TOKEN` in Waygate's `.env` or environment.
2. Like `MANAGEMENT_API_TOKEN`, `ADMIN_API_TOKEN` must be at least 32 printable ASCII characters without spaces. When empty, all `/api/v1/admin/...` routes return HTTP 404 (`not_found`).
3. If both `MANAGEMENT_API_TOKEN` and `ADMIN_API_TOKEN` are configured, they **must be distinct**. Startup fails if they are identical.
4. `MANAGEMENT_API_TOKEN` remains strictly read-only. Presenting `MANAGEMENT_API_TOKEN` to `/api/v1/admin/...` returns HTTP 403 (`insufficient_scope`). Presenting `ADMIN_API_TOKEN` authorizes both `/api/v1/admin/...` and the read-only `/api/v1/...` dashboard endpoints.
5. **Actor Attribution**: Admin API mutations record `actor = "api"` in the staff audit log (`staff_events`). Callers may optionally supply `X-Admin-Actor: <name>` (matching `^[A-Za-z0-9._-]{1,32}$`) to record `actor = "api:<name>"`.
6. **Effective Rank Ceiling**: The Admin API operates at effective GM rank `4` (Super GM), allowing it to inspect and manage `Player (0)`, `Moderator (1)`, `GM (2)`, and `Admin (3)` accounts, and to grant ranks `0`, `1`, `2`, or `3` (never `4`).

All Admin API responses return `{"data": ...}` on success and `{"error": {"code": "...", "message": "..."}}` on error, with `Cache-Control: no-store` and no session cookies.

## Administrative Resources

### 1. Accounts & Access
| Method & Path | Description |
| --- | --- |
| `GET /api/v1/admin/accounts` | List/search accounts (`?q=&bots=0\|1&limit=40&offset=0`) with Gatehouse links |
| `GET /api/v1/admin/accounts/{username}` | Inspect a WoW or Gatehouse account, identity MFA/passkey metadata, linked logins, and characters |
| `POST /api/v1/admin/accounts` | Create a new Gatehouse + WoW account (`{"username", "password", "email", "expansion"}`) |
| `POST /api/v1/admin/accounts/{username}/password` | Reset password (`{"password", "sync_gatehouse": true}`) and revoke active sessions |
| `PUT /api/v1/admin/accounts/{username}/rank` | Set rank `0..3` (`{"rank": 0\|1\|2\|3}`) across Gatehouse and linked WoW logins |
| `POST /api/v1/admin/accounts/{username}/ban` | Suspend account and linked logins (`{"duration": "1h"\|"1d"\|"7d"\|"30d"\|"permanent", "reason": "..."}`) |
| `DELETE /api/v1/admin/accounts/{username}/ban` | Lift suspension across linked WoW logins |

### 2. Portal Operations
| Method & Path | Description |
| --- | --- |
| `GET /api/v1/admin/banner` | Get current maintenance banner (`active`, `message`, `until`) |
| `PUT /api/v1/admin/banner` | Set maintenance banner (`{"message": "...", "until": "2026-11-01T04:00:00Z"}`) |
| `DELETE /api/v1/admin/banner` | Clear maintenance banner |
| `GET /api/v1/admin/register-key` | Get registration key configuration (`required`, `register_key`) |
| `PUT /api/v1/admin/register-key` | Set or clear registration key (`{"register_key": "..."}`) |
| `DELETE /api/v1/admin/register-key` | Open public registration (clear registration key) |
| `GET /api/v1/admin/calendar` | List realm calendar events (`?limit=20`) |
| `POST /api/v1/admin/calendar` | Create calendar event (`{"date": "YYYY-MM-DD", "title": "...", "detail": "..."}`) |
| `DELETE /api/v1/admin/calendar/{id}` | Delete calendar event by ID |
| `GET /api/v1/admin/audit` | List recent staff audit events (`?limit=200`) |

### 3. Support Tickets
| Method & Path | Description |
| --- | --- |
| `GET /api/v1/admin/tickets` | List tickets (`?status=active\|open\|in-progress\|done\|closed\|all&account_id=&limit=50&offset=0`) |
| `GET /api/v1/admin/tickets/{id}` | Get full ticket and message history |
| `PATCH /api/v1/admin/tickets/{id}` | Update status (`open`, `in-progress`, `done`, `closed`) and/or post staff reply (`{"status", "body"}`) |

### 4. Knowledge Base & Downloads
| Method & Path | Description |
| --- | --- |
| `GET /api/v1/admin/kb` | List all Knowledge Base articles (including drafts) and categories |
| `GET /api/v1/admin/kb/{id}` | Get article by numeric ID or slug (includes `body_markdown` and rendered `html`) |
| `POST /api/v1/admin/kb` | Create article (`{"title", "slug", "category", "summary", "body_markdown", "sort_order", "published"}`) |
| `PUT /api/v1/admin/kb/{id}` | Update article by numeric ID |
| `DELETE /api/v1/admin/kb/{id}` | Delete article by numeric ID |
| `GET /api/v1/admin/downloads` | List downloads catalog (`?category=`) and storage/scanner status |
| `POST /api/v1/admin/downloads` | Upload archive via `multipart/form-data` (`file`, `category`, `title`, `version`, `description`, `source_url`, `addon_id`, `mandatory`) |
| `DELETE /api/v1/admin/downloads/{id}` | Remove download entry and underlying file |

### 5. In-Game / Worldserver Actions
| Method & Path | Description |
| --- | --- |
| `POST /api/v1/admin/game/unstuck` | Unstuck offline character by `guid` or `name` (`{"guid": 102}` or `{"name": "Thassarian"}`) |
| `POST /api/v1/admin/game/announce` | Broadcast worldserver message (`{"message": "...", "mode": "announce"\|"notify"\|"both"}`) |
| `POST /api/v1/admin/game/soap` | Execute non-credential worldserver SOAP command (`{"command": "server info"}`) |

