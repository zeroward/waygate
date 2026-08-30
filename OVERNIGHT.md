# Overnight issue work

## #2 Home: latest published KB

**Shipped:** Newest published Knowledge Base article (by `updated_at`) is pinned on `/` under the population metrics: category, title linking to `/kb/:slug`, one-line summary. Hidden when none are published. Drafts are excluded. No extra nav item.

**Files:** `internal/kb/store.go`, `internal/kb/store_test.go`, `internal/web/handlers.go`, `internal/web/templates/home.html`, `internal/web/static/css/app.css`, `internal/web/templates/header.html`, `internal/web/register_test.go`

**Verify:** `GET /` includes `How to connect` and `/kb/how-to-connect`. After deleting the seed article, home is still 200 without that link.

**Leftover:** None for this issue.

## #3 Leaderboards: gold tab

**Shipped:** `/leaderboards?tab=gold` ranks real player characters by `characters.money`, formatted with `wow.Gold`. Bots (`rndbot*`) stay off the board. Character name **Auctioneer** is also excluded (AH bot). Demo snapshot includes a gold board. No Armory links.

**Files:** `internal/status/status.go`, `internal/status/status_test.go`, `internal/web/handlers.go`, `internal/web/templates/leaderboards.html`, `internal/web/register_test.go`

**Verify:** `GET /leaderboards?tab=gold` is 200, shows Frostwarden and `1234g`, no `RNDBOT` or `/armory`.

**Leftover:** Other AH-bot names can be added to `boardSkipNames` if they show up.

## #4 Admin panel: staff action log

**Shipped:** SQLite `staff_events` in the Gatehouse KB database (not WoW MySQL). Successful unstuck, staff create/reset/rank, download upload/delete, and KB create/update/delete insert a row. `/staff` shows time, actor, action, target (newest first). Pruned to 200 rows and 30 days.

**Files:** `internal/kb/events.go`, `internal/kb/store.go`, `internal/kb/store_test.go`, `internal/web/handlers.go`, `internal/web/kb_handlers.go`, `internal/web/staff_downloads.go`, `internal/web/templates/staff.html`, `internal/web/staff_test.go`

**Verify:** After creating an account from Admin panel, GET `/staff` contains `Recent actions` and `create`.

**Leftover:** None for this issue.

## #5 Player tickets

**Shipped:** In-app tickets in Gatehouse SQLite (`tickets` + `ticket_messages`), not WoW DBs. Logged-in `/tickets` lists yours, `/tickets/new` opens one (categories Name change / Character transfer/restore / Guild / Items / Other, optional owned-character picker), `/tickets/{id}` is the thread. Staff at `GM_MIN_LEVEL` use `/staff/tickets` for open + in-progress, reply, and set Open → In progress → Done/Closed. Player cannot edit the original; comments allowed while Open/In progress. CSRF on writes; `RATE_LIMIT_TICKETS` (default 5) on opens; bodies go through html/template (escaped). No SOAP, Discord, attachments, or email.

**Files:** `internal/kb/tickets.go`, `internal/kb/store.go`, `internal/kb/store_test.go`, `internal/config/config.go`, `internal/web/server.go`, `internal/web/ticket_handlers.go`, `internal/web/ticket_test.go`, `internal/web/templates/tickets.html`, `internal/web/templates/ticket_new.html`, `internal/web/templates/ticket_view.html`, `internal/web/templates/staff_tickets.html`, `internal/web/templates/header.html`, `internal/web/templates/staff.html`, `internal/web/templates/account.html`, `internal/web/static/css/app.css`, `.env.example`

**Verify:** Anon `GET /tickets` redirects to login. Player A cannot `GET` player B’s ticket (404). CSRF missing → 403. Staff reply + status; Done leaves the open queue; player cannot comment after close. Second open with `RATE_LIMIT_TICKETS=1` bounces to `/tickets/new`.

**Leftover:** Staff can still open a ticket by ID after it is Done/Closed. No attachments.

## #1 Armory (login-gated inspect)

**Shipped:** Login-gated Armory. `GET /armory` searches by name (no `rndbot*` accounts, no deleted). `GET /armory/{name}` inspects with `?tab=` Sheet / Gear / Talents / Achievements / PvP. Gear is equipped slots 0–18 (`bag=0`) with `item_template` names/quality and plain Wowhead WotLK item links. Talents are both specs plus glyphs. Achievements use a static 3.3.5a name map (no DBC at runtime). Arena teams are 2s/3s/5s. Nav Armory sits after Leaderboards. Account, Online, and Leaderboards names link in. Demo Frostwarden has full inspect data. CSP stays `script-src 'self'`. SELECT-only grants documented. No 3D viewer.

**Files:** `internal/armory/*`, `internal/status/status.go`, `internal/web/armory_handlers.go`, `internal/web/armory_test.go`, `internal/web/server.go`, `internal/web/templates/armory.html`, `internal/web/templates/armory_char.html`, `internal/web/templates/header.html`, `internal/web/templates/account.html`, `internal/web/templates/online.html`, `internal/web/templates/leaderboards.html`, `internal/web/static/css/app.css`, `internal/web/register_test.go`, `docs/mysql-grants.sql`

**Verify:** Anon `/armory` and `/armory/Frostwarden` redirect to login with `next=`. Logged-in `/armory/Frostwarden` is 200 (sheet, gear with Wowhead, talents, Level 80 achievement, 2v2/3v3). CSP includes `script-src 'self'`.

**Leftover:** Talent/glyph name maps are a subset; unknown IDs print `Talent N` / `Glyph N`. Live MySQL GRANTs still need to be applied on the server (`docs/mysql-grants.sql`).

