# How to connect

This realm runs **{{CORE_NAME}}** (client build **12340**).

## 1. Get a 3.3.5a client

Download the client from the [Downloads](/downloads) tab (and any patches or mods you want). Use a clean Wrath of the Lich King client, patch 3.3.5a, build 12340. Retail, Classic Era, and Cataclysm clients will not work.

## 2. Set your realmlist

Edit `Data/enUS/realmlist.wtf` (or `enGB`, `deDE`, `ruRU`, … for your locale):

```
set realmlist {{PUBLIC_HOST}}
```

Save the file, then start `Wow.exe`. Do not use the launcher.

## 3. Ports

- Auth (login server): `{{PUBLIC_HOST}}:{{PUBLIC_AUTH_PORT}}`
- World (the realm itself): `{{PUBLIC_HOST}}:{{PUBLIC_WORLD_PORT}}`

This host publishes world on **{{PUBLIC_WORLD_PORT}}**, not the core default 8085. If you cannot see the realm after logging in, your client or firewall is probably still aiming at 8085.

## 4. Create an account

Register on this site, then use the **same username and password** at the WoW login screen. Usernames are case-insensitive.

## 5. Notes

- Expansion: Wrath of the Lich King.
- Random playerbots (`rndbot*`) may populate cities and the open world. They are **not** counted as real players on this website.
- Addons built for 3.3.5a are fine. Do not install retail addons.
