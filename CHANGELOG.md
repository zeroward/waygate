# Changelog

Versioning is **SemVer**. Anything `0.x` is unstable. Pre-releases use `-alpha.N`, `-beta.N`, or `-rc.N`. **1.0.0** is reserved until the site has had a real security pass (captcha, HTTPS cookies, SOAP, upload scanning, session store).

## [0.1.0-alpha.1] — 2026-08-30

First tagged Gatehouse (waygate) drop. Feature-complete enough to run a private WotLK realm portal; **not** security-reviewed.

### Added

- Account registration and website login with AzerothCore SRP6 (SOAP preferred, SQL fallback)
- Staff admin panel (create, password reset, GM/Admin rank; never Super GM)
- Knowledge Base (`/kb`) with GM 3+ editor, markdown, CSRF, rate limits
- Downloads catalog (client / mandatory patches / optional patches / featured addons / addons)
- Account character list and **Unstuck** to hearth/homebind (offline only)
- Downloadable `realmlist.wtf`
- Online roster and leaderboards (bots filtered; GMs shown by default)
- Contact form, password reset mail, Icecrown/Chunguscraft chrome

### Security status (alpha)

- Live `CAPTCHA_PROVIDER=none` is still a registration risk
- Website sessions are in-memory (one replica, lost on restart)
- ClamAV is installed but **not** scanning uploads
- SOAP may be unset (SQL SRP6 / SQL unstuck fallback)
- No pentest, no threat model sign-off

### Container

`ghcr.io/zeroward/waygate:0.1.0-alpha.1`
