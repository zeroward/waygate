-- Least-privilege MySQL user for Gatehouse (waygate).
-- The database account name can stay `webreg`; it is not the app name.
-- Run as a MySQL admin against the same instance as ac-database.
-- Replace 'change-me' and the host ('%') before using.

CREATE USER IF NOT EXISTS 'webreg'@'%' IDENTIFIED BY 'change-me';

-- Status pages, uniqueness checks, website login (SRP6 verify).
GRANT SELECT ON acore_auth.account TO 'webreg'@'%';
GRANT SELECT ON acore_auth.account_access TO 'webreg'@'%';
GRANT SELECT ON acore_auth.realmlist TO 'webreg'@'%';
GRANT SELECT ON acore_characters.characters TO 'webreg'@'%';
GRANT SELECT ON acore_characters.character_homebind TO 'webreg'@'%';
-- Armory inspect (apply on existing servers too; CREATE USER does not add these).
GRANT SELECT ON acore_characters.character_inventory TO 'webreg'@'%';
GRANT SELECT ON acore_characters.item_instance TO 'webreg'@'%';
GRANT SELECT ON acore_characters.character_talent TO 'webreg'@'%';
GRANT SELECT ON acore_characters.character_glyphs TO 'webreg'@'%';
GRANT SELECT ON acore_characters.character_achievement TO 'webreg'@'%';
GRANT SELECT ON acore_characters.guild TO 'webreg'@'%';
GRANT SELECT ON acore_characters.guild_member TO 'webreg'@'%';
GRANT SELECT ON acore_characters.arena_team TO 'webreg'@'%';
GRANT SELECT ON acore_characters.arena_team_member TO 'webreg'@'%';
-- Account unstuck (hearth/homebind) when SOAP is down. Column-level UPDATE only.
REVOKE UPDATE ON acore_characters.characters FROM 'webreg'@'%';
GRANT UPDATE (`position_x`, `position_y`, `position_z`, `orientation`, `map`, `zone`,
  `trans_x`, `trans_y`, `trans_z`, `transguid`, `taxi_path`, `cinematic`, `playerFlags`, `at_login`)
  ON acore_characters.characters TO 'webreg'@'%';

-- Optional world revision string on the home page.
GRANT SELECT ON acore_world.version TO 'webreg'@'%';
GRANT SELECT ON acore_world.module_string TO 'webreg'@'%';
GRANT SELECT ON acore_world.item_template TO 'webreg'@'%';
-- Companion quest tracker (apply on existing servers too).
GRANT SELECT ON acore_characters.character_queststatus TO 'webreg'@'%';
GRANT SELECT ON acore_characters.character_queststatus_rewarded TO 'webreg'@'%';
GRANT SELECT ON acore_world.quest_template TO 'webreg'@'%';
GRANT SELECT ON acore_world.quest_template_addon TO 'webreg'@'%';
GRANT SELECT ON acore_world.quest_poi_points TO 'webreg'@'%';

-- Required only for ACCOUNT_CREATE_MODE=sql or auto (SRP6 fallback),
-- password change fallback, and filling email/expansion after SOAP create.
GRANT INSERT, UPDATE ON acore_auth.account TO 'webreg'@'%';

-- Rank changes from the Admin panel when SOAP is down (Player / GM / Admin only; never Super GM).
GRANT INSERT, UPDATE, DELETE ON acore_auth.account_access TO 'webreg'@'%';

-- Account suspend/unban from the Admin panel (SOAP first; SQL fallback).
GRANT SELECT, INSERT, UPDATE ON acore_auth.account_banned TO 'webreg'@'%';

-- Never grant DROP, GRANT, FILE, PROCESS, SUPER, or DELETE on acore_auth.account.

FLUSH PRIVILEGES;

-- SOAP-only operators can skip INSERT/UPDATE if they accept that
-- email/expansion will not be patched from the website after create:
--   REVOKE INSERT, UPDATE ON acore_auth.account FROM 'webreg'@'%';
