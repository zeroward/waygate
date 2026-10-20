# Waygate (Gatehouse) Management & Admin API v1 — LLM Reference Specification

> **Purpose**: Self-contained, machine- and LLM-ingestible specification for all Waygate HTTP API endpoints (`/api/v1/*` and `/api/v1/admin/*`).

---

## 1. Global Rules & Authentication

- **Base URL**: `<WAYGATE_URL>` (e.g. `https://portal.example.com` or `http://127.0.0.1:3080`)
- **Path Matching**: Exact paths only; **never append a trailing slash** (trailing slashes return `404 not_found`).
- **Headers**:
  - `Authorization: Bearer <TOKEN>` (required on all `/api/*` requests; query-string tokens and cookies are ignored/rejected).
  - `Content-Type: application/json` (for all `POST`, `PUT`, `PATCH` JSON endpoints; unknown JSON fields are rejected with `400 invalid_json`).
  - `X-Admin-Actor: <sub-actor>` (optional on `/api/v1/admin/*`; must match `^[A-Za-z0-9._-]{1,32}$`; records actor as `api:<sub-actor>` instead of `api` in the staff audit log).
- **Tokens & Scopes**:
  - `MANAGEMENT_API_TOKEN`: Authorizes **only** read-only endpoints (`/api/v1/{dashboard,status,players,leaderboards,modules}`). Returns `403 insufficient_scope` if used on `/api/v1/admin/*`.
  - `ADMIN_API_TOKEN`: Authorizes **both** `/api/v1/admin/*` and read-only `/api/v1/*` endpoints. Must be $\ge 32$ printable ASCII characters without spaces and distinct from `MANAGEMENT_API_TOKEN`.
- **Effective Rank**: The Admin API acts with effective GM level `4` (`Super GM`). It can inspect and modify `Player (0)`, `Moderator (1)`, `GM (2)`, and `Admin (3)` accounts, and can grant ranks `0`, `1`, `2`, or `3` (never `4`).
- **Error Envelope** (all non-2xx responses):
  ```json
  {
    "error": {
      "code": "string_error_code",
      "message": "Human-readable description."
    }
  }
  ```

---

## 2. Read-Only Dashboard Endpoints (`/api/v1/*`)

Supported methods: `GET`, `HEAD`.

###Read-Only Envelope
```json
{
  "generated_at": "2026-10-19T18:00:00Z",
  "cache_ttl_seconds": 20,
  "demo": false,
  "partial": false,
  "data": {},
  "errors": []
}
```

| Endpoint | `data` Type & Shape |
| --- | --- |
| `GET /api/v1/dashboard` | `{ "status": Status\|null, "players": Player[]\|null, "leaderboards": Leaderboards\|null, "modules": Module[]\|null }` |
| `GET /api/v1/status` | `{ "realm_name": string, "realm_up": bool, "core_name": string, "expansion": string, "world_version": string, "public_host": string, "public_auth_port": int, "public_world_port": int, "population": { "humans": int, "bots": int, "gms": int, "total": int, "alliance": int, "horde": int } }` |
| `GET /api/v1/players` | `Array<{ "name": string, "level": uint8, "race": string, "race_id": uint8, "class": string, "class_id": uint8, "zone": string, "faction": "Alliance"\|"Horde" }>` |
| `GET /api/v1/leaderboards` | `{ "playtime": BoardRow[], "kills": BoardRow[], "honor": BoardRow[], "arena": BoardRow[], "gold": BoardRow[] }` where `BoardRow = { "rank": int, "name": string, "level": uint8, "class": string, "value": string, "raw": uint32 }` |
| `GET /api/v1/modules` | `Array<{ "id": string, "title": string }>` |

---

## 3. Programmatic Administration Endpoints (`/api/v1/admin/*`)

All successful responses return:
```json
{
  "data": { ... }
}
```

---

### Domain A: Accounts & Access

#### A1. List / Search Accounts
- **Method & Path**: `GET /api/v1/admin/accounts` (also `HEAD`)
- **Query Parameters**:
  - `q` (string, optional): Case-insensitive substring match on username or email (max 32 chars).
  - `bots` (`"0"` | `"1"` | `"true"` | `"false"`, default `"0"`): Include playerbot accounts matching `BOT_USERNAME_PREFIXES`.
  - `limit` (int, `1..100`, default `40`): Page size.
  - `offset` (int, `>= 0`, default `0`): Pagination offset.
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "accounts": [
        {
          "id": 12,
          "username": "ARTHAS",
          "email": "arthas@lordaeron.example",
          "join_date": "2026-10-19 18:00",
          "last_login": "2026-10-19 19:00",
          "last_ip": "127.0.0.1",
          "online": false,
          "expansion": 2,
          "gm_level": 3,
          "rank_name": "Admin",
          "locked": false,
          "banned": false,
          "ban_reason": "",
          "ban_until": "",
          "gatehouse": "ARTHAS",
          "linked": ["ARTHAS"]
        }
      ],
      "total": 1,
      "limit": 40,
      "offset": 0
    }
  }
  ```

#### A2. Inspect Account
- **Method & Path**: `GET /api/v1/admin/accounts/{username}` (also `HEAD`)
- **Path Parameters**: `username` (WoW or Gatehouse username, 3..16 ASCII alphanumeric chars).
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "account": {
        "id": 12,
        "username": "ARTHAS",
        "email": "arthas@lordaeron.example",
        "join_date": "2026-10-19 18:00",
        "last_login": "—",
        "last_ip": "",
        "online": false,
        "expansion": 2,
        "gm_level": 3,
        "rank_name": "Admin",
        "locked": false,
        "banned": false,
        "gatehouse": "ARTHAS",
        "linked": ["ARTHAS"]
      },
      "identity": {
        "id": 4,
        "username": "ARTHAS",
        "email": "arthas@lordaeron.example",
        "staff_level": 3,
        "rank_name": "Admin",
        "auth_mode": "local",
        "totp_enabled": false,
        "passkey_count": 1
      },
      "linked_logins": ["ARTHAS"],
      "characters": [
        {
          "guid": 2,
          "account_id": 12,
          "login": "ARTHAS",
          "name": "NorthrendScout",
          "level": 77,
          "race": "Night Elf",
          "race_id": 4,
          "class": "Hunter",
          "class_id": 3,
          "location": "Borean Tundra",
          "faction": "Alliance",
          "gold": "88g 12s",
          "played": "4d 17h",
          "online": false
        }
      ]
    }
  }
  ```
- **Errors**: `400 invalid_username`, `404 account_not_found`.

#### A3. Create Account
- **Method & Path**: `POST /api/v1/admin/accounts`
- **Request Body**:
  ```json
  {
    "username": "Arthas",
    "password": "FrostPass99",
    "email": "arthas@lordaeron.example",
    "expansion": 2
  }
  ```
  - `username`: 3..16 ASCII letters/digits, must start with a letter.
  - `password`: 8..16 printable ASCII chars (cannot contain `"` or `\` or equal username).
  - `email`: optional valid email address.
  - `expansion`: optional `0` (Classic), `1` (TBC), or `2` (WotLK, default).
- **Response (`201 Created`)**: `{ "data": <AdminAccount> }`
- **Errors**: `400 invalid_username` | `invalid_password` | `invalid_email` | `invalid_expansion`, `409 username_taken` | `email_taken`.

#### A4. Reset Account Password
- **Method & Path**: `POST /api/v1/admin/accounts/{username}/password`
- **Request Body**:
  ```json
  {
    "password": "NewFrozenPass99",
    "sync_gatehouse": true
  }
  ```
  - `password`: 8..16 printable ASCII chars.
  - `sync_gatehouse`: optional boolean (default `true`). When `true`, also updates the linked Gatehouse website password (if local auth) and revokes active browser sessions.
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "username": "ARTHAS",
      "updated": true,
      "gatehouse_updated": true
    }
  }
  ```
- **Errors**: `400 invalid_username` | `invalid_password`, `403 forbidden`, `404 account_not_found`.

#### A5. Set Account & Identity Rank
- **Method & Path**: `PUT /api/v1/admin/accounts/{username}/rank`
- **Request Body**:
  ```json
  {
    "rank": 2
  }
  ```
  - `rank`: `0` (Player), `1` (Moderator), `2` (GM), or `3` (Admin). `4` (Super GM) is rejected.
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "username": "ARTHAS",
      "gatehouse": "ARTHAS",
      "rank": 2,
      "rank_name": "GM",
      "updated_logins": 1
    }
  }
  ```
- **Errors**: `400 invalid_rank`, `403 forbidden`, `404 account_not_found`.

#### A6. Suspend (Ban) Account
- **Method & Path**: `POST /api/v1/admin/accounts/{username}/ban`
- **Request Body**:
  ```json
  {
    "duration": "7d",
    "reason": "Botting in Icecrown"
  }
  ```
  - `duration`: `"1h"`, `"1d"`, `"7d"`, `"30d"`, `"permanent"` (or `"perm"`, `"-1"`).
  - `reason`: 3..255 characters.
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "target": "ARTHAS",
      "duration": "7 days",
      "reason": "Botting in Icecrown",
      "suspended_logins": 1
    }
  }
  ```
- **Errors**: `400 invalid_duration` | `invalid_reason`, `403 forbidden`, `404 account_not_found`.

#### A7. Unban Account
- **Method & Path**: `DELETE /api/v1/admin/accounts/{username}/ban`
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "target": "ARTHAS",
      "unbanned_logins": 1
    }
  }
  ```
- **Errors**: `400 invalid_username`, `403 forbidden`, `404 account_not_found`.

---

### Domain B: Portal Operations

#### B1. Maintenance Banner
- **`GET /api/v1/admin/banner`** (also `HEAD`):
  ```json
  {
    "data": {
      "active": "Scheduled maintenance at 02:00 UTC",
      "message": "Scheduled maintenance at 02:00 UTC",
      "until": "2026-11-01T04:00:00Z"
    }
  }
  ```
- **`PUT /api/v1/admin/banner`**:
  - Request: `{ "message": "Scheduled maintenance at 02:00 UTC", "until": "2026-11-01T04:00:00Z" }` (`until` optional RFC 3339 or `YYYY-MM-DDTHH:MM` UTC; empty `message` clears banner).
  - Response (`200 OK`): Same as `GET`.
- **`DELETE /api/v1/admin/banner`**:
  - Clears banner and returns `{ "data": { "active": "", "message": "", "until": "" } }`.

#### B2. Registration Key
- **`GET /api/v1/admin/register-key`** (also `HEAD`):
  ```json
  {
    "data": {
      "required": true,
      "register_key": "icetime-2026"
    }
  }
  ```
- **`PUT /api/v1/admin/register-key`**:
  - Request: `{ "register_key": "icetime-2026" }` (4..64 printable ASCII characters without spaces, or `""` to open registration).
  - Response (`200 OK`): Same as `GET`.
- **`DELETE /api/v1/admin/register-key`**:
  - Clears key and returns `{ "data": { "required": false, "register_key": "" } }`.

#### B3. Realm Calendar Events
- **`GET /api/v1/admin/calendar?limit=20`** (also `HEAD`, `limit` `1..50`):
  ```json
  {
    "data": {
      "events": [
        {
          "id": 1,
          "date": "2026-11-01",
          "title": "Ulduar Timewalking",
          "detail": "25-man hard modes at 19:00 UTC",
          "created_by": "api:ops",
          "created_at": "2026-10-19T19:00:00Z"
        }
      ]
    }
  }
  ```
- **`POST /api/v1/admin/calendar`**:
  - Request: `{ "date": "2026-11-01", "title": "Ulduar Timewalking", "detail": "25-man hard modes at 19:00 UTC" }` (`date` `YYYY-MM-DD`, `title` 1..80 chars, `detail` 0..280 chars).
  - Response (`201 Created`): `{ "data": <AdminCalendarEvent> }`.
- **`DELETE /api/v1/admin/calendar/{id}`**:
  - Response (`200 OK`): `{ "data": { "deleted": true, "id": 1 } }`.

#### B4. Staff & API Audit Log
- **`GET /api/v1/admin/audit?limit=200`** (also `HEAD`, `limit` `1..200`):
  ```json
  {
    "data": {
      "events": [
        {
          "at": "2026-10-19T19:00:00Z",
          "actor": "api:ops",
          "action": "event-add",
          "target": "2026-11-01 Ulduar Timewalking"
        }
      ]
    }
  }
  ```

---

### Domain C: Support Tickets

#### C1. List Support Tickets
- **Method & Path**: `GET /api/v1/admin/tickets` (also `HEAD`)
- **Query Parameters**:
  - `status`: `"active"` (default, open + in-progress), `"open"`, `"in-progress"`, `"done"`, `"closed"`, or `"all"`.
  - `account_id`: optional positive integer.
  - `limit`: `1..100` (default `50`).
  - `offset`: `>= 0` (default `0`).
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "tickets": [
        {
          "id": 1,
          "public_ref": "T-A1B2C3",
          "account_id": 1,
          "username": "HEROONE",
          "category": "Items",
          "title": "Missing Ashbringer",
          "character_guid": 0,
          "character_name": "",
          "status": "open",
          "created_at": "2026-10-19T18:30:00Z",
          "updated_at": "2026-10-19T18:30:00Z",
          "closed_by": ""
        }
      ],
      "total": 1,
      "limit": 50,
      "offset": 0
    }
  }
  ```

#### C2. Get Ticket & Message Thread
- **Method & Path**: `GET /api/v1/admin/tickets/{id}` (also `HEAD`)
- **Response (`200 OK`)**: Same fields as above plus `"messages": [{ "id": 1, "ticket_id": 1, "author": "HEROONE", "from_staff": false, "body": "Lost my sword.", "created_at": "2026-10-19T18:30:00Z" }]`.

#### C3. Reply / Update Ticket Status
- **Method & Path**: `PATCH /api/v1/admin/tickets/{id}`
- **Request Body** (provide `status`, `body`, or both):
  ```json
  {
    "status": "done",
    "body": "Restored via in-game mail."
  }
  ```
  - `status`: `"open"`, `"in-progress"`, `"done"`, or `"closed"`.
  - `body`: 1..4000 characters.
- **Response (`200 OK`)**: `{ "data": <AdminTicketWithMessages> }`.

---

### Domain D: Content & Downloads (Knowledge Base & Downloads)

#### D1. List Knowledge Base Articles
- **Method & Path**: `GET /api/v1/admin/kb` (also `HEAD`)
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "articles": [
        {
          "id": 1,
          "slug": "how-to-connect",
          "title": "How to connect",
          "summary": "Get a 3.3.5a client, set the realmlist, and start Wow.exe.",
          "category": "Getting started",
          "sort_order": 0,
          "published": true,
          "created_by": "system",
          "updated_by": "system",
          "created_at": "2026-10-19T18:00:00Z",
          "updated_at": "2026-10-19T18:00:00Z"
        }
      ],
      "categories": ["Getting started"]
    }
  }
  ```

#### D2. Get Knowledge Base Article (by ID or Slug)
- **Method & Path**: `GET /api/v1/admin/kb/{id}` (where `{id}` is numeric ID or slug like `how-to-connect`)
- **Response (`200 OK`)**: Includes `"body_markdown"` and rendered `"html"`.

#### D3. Create / Update / Delete Knowledge Base Article
- **Create**: `POST /api/v1/admin/kb` -> `201 Created`
- **Update**: `PUT /api/v1/admin/kb/{id}` (numeric ID) -> `200 OK`
  - Request Body:
    ```json
    {
      "title": "Raid Rules",
      "slug": "raid-rules",
      "category": "Policies",
      "summary": "Loot and lockout rules.",
      "body_markdown": "## Loot\nBe respectful.",
      "sort_order": 1,
      "published": true
    }
    ```
- **Delete**: `DELETE /api/v1/admin/kb/{id}` (numeric ID) -> `200 OK` `{ "data": { "deleted": true, "id": 2 } }`.

#### D4. Downloads Catalog
- **List**: `GET /api/v1/admin/downloads?category=mods` (`category` optional: `client`, `patches`, `mods`).
- **Upload**: `POST /api/v1/admin/downloads` (`multipart/form-data` with `file` binary part and fields `category`, `title`, `version`, `description`, `source_url`, `addon_id`, `mandatory`). Allowed extensions: `.zip`, `.7z`, `.rar`, `.mpq`, `.patch`.
- **Delete**: `DELETE /api/v1/admin/downloads/{id}` -> `200 OK` `{ "data": { "deleted": true, "id": "mods-dbm" } }`.

---

### Domain E: In-Game / Worldserver Actions

#### E1. Unstuck Offline Character
- **Method & Path**: `POST /api/v1/admin/game/unstuck`
- **Request Body** (supply `guid` or `name`):
  ```json
  {
    "name": "NorthrendScout"
  }
  ```
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "guid": 2,
      "name": "NorthrendScout",
      "from_map": 571,
      "from_zone": 3537,
      "to_map": 571,
      "to_zone": 4395,
      "via": "soap"
    }
  }
  ```
- **Errors**: `404 character_not_found`, `409 character_online`, `422 no_homebind`.

#### E2. Broadcast Worldserver Announcement
- **Method & Path**: `POST /api/v1/admin/game/announce`
- **Request Body**:
  ```json
  {
    "message": "Server restart in 15 minutes.",
    "mode": "both"
  }
  ```
  - `mode`: `"announce"` (default chat announcement), `"notify"` (screen center notification), or `"both"`.
- **Response (`200 OK`)**: `{ "data": { "sent": true, "mode": "both", "message": "Server restart in 15 minutes." } }`.

#### E3. Execute Worldserver SOAP Command
- **Method & Path**: `POST /api/v1/admin/game/soap`
- **Request Body**:
  ```json
  {
    "command": "server info"
  }
  ```
  - **Safety Constraint**: Credential and account-lifecycle commands (`account create`, `account password`, `account set password`, `account set gmlevel`, `account set sec`, `account onetimetoken`, `account delete`) are rejected with `400 invalid_command`; use `/api/v1/admin/accounts/*` instead.
- **Response (`200 OK`)**:
  ```json
  {
    "data": {
      "command": "server info",
      "result": "AzerothCore rev. 2026..."
    }
  }
  ```
