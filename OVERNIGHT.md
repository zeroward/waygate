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

