# How to connect

This realm runs **{{CORE_NAME}}** (client build **12340**).

## 1. Get a 3.3.5a client

Download the client from the [Downloads](/downloads) tab (and any patches or mods you want). Use a clean Wrath of the Lich King client, patch 3.3.5a, build 12340. Retail, Classic Era, and Cataclysm clients will not work.

## 2. Connect through NetBird

Use the NetBird setup link on your Account page, or ask your administrator for enrollment instructions. Sign in to NetBird and connect before starting the game. Device enrollment and network access are managed outside this website.

## 3. Set your realmlist

Edit `Data/enUS/realmlist.wtf` (or `enGB`, `deDE`, `ruRU`, … for your locale):

```
set realmlist {{PUBLIC_HOST}}
```

Save the file, then start `Wow.exe`. Do not use the launcher.

## 4. Ports

- Auth (login server): `{{PUBLIC_HOST}}:{{PUBLIC_AUTH_PORT}}`
- World (the realm itself): `{{PUBLIC_HOST}}:{{PUBLIC_WORLD_PORT}}`

This host publishes world on **{{PUBLIC_WORLD_PORT}}**, not the core default 8085. If you cannot see the realm after logging in, your client or firewall is probably still aiming at 8085.

## 5. Create your game login

Sign in to this website and open **Account → WoW client logins**. Create a client username and password there, then use those credentials in Wow.exe. SSO signs you into the website; the game client still needs its own username and password. Usernames are case-insensitive.

## 6. Notes

- Expansion: Wrath of the Lich King.
- Random playerbots (`rndbot*`) may populate cities and the open world. They are **not** counted as real players on this website.
- Addons built for 3.3.5a are fine. Do not install retail addons.
