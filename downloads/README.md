# Downloads catalog

This directory is what the **Downloads** tab reads. Admins can upload and remove files from the **Admin panel** (`/staff`) without editing the server by hand.

```
downloads/
  catalog.json          # titles, descriptions, checksums
  client/               # the 3.3.5a .zip
  patches/              # optional patches
  mods/                 # optional mods / addons
```

## Upload from the Admin panel

1. Log in as a GM (`gmlevel` ≥ `GM_MIN_LEVEL`).
2. Open **Admin panel**, scroll to **Downloads**.
3. Choose a file (`.zip` `.7z` `.rar` `.mpq` `.patch` `.exe`), category, optional title/description, then **Upload**.

The file is stored here and listed on `/downloads` immediately after a **ClamAV** scan (when `CLAMAV_ADDR` is set). Infected files are discarded. If clamd is down, the upload is rejected. **Remove** deletes the file and its catalog row. SHA-256 is computed on upload.

The Docker volume must be writable. Official compose mounts `./downloads` read-write and runs as `WEBREG_UID`/`WEBREG_GID` (default `1000:1000`, matching a typical host owner). If uploads fail with “not writable”, match those IDs to `ls -ln downloads`.

## Add the client zip (host copy)

1. Copy the archive onto the host:

   ```bash
   cp /path/to/WoW-3.3.5a.zip downloads/client/WoW-3.3.5a.zip
   ```

   The filename should match `file` in `catalog.json` (`client/WoW-3.3.5a.zip`).

2. Optional checksum sidecar (or set `sha256` in the catalog):

   ```bash
   sha256sum downloads/client/WoW-3.3.5a.zip | awk '{print $1}' > downloads/client/WoW-3.3.5a.zip.sha256
   ```

3. Reload `/downloads` — the catalog is re-read every few seconds. No restart needed.

## Add a patch or mod

Drop a `.zip` / `.7z` / `.rar` / `.mpq` / `.patch` / `.exe` into `patches/` or `mods/`. It appears even without a catalog entry (title comes from the filename).

To give it a proper name and description, add an object to `catalog.json`:

```json
{
  "id": "mod-dbm",
  "title": "Deadly Boss Mods",
  "category": "mods",
  "file": "mods/DBM.zip",
  "description": "3.3.5a boss timers.",
  "sha256": ""
}
```

`id` is the download URL: `/downloads/mod-dbm`. Use lowercase letters, digits, and hyphens.

## Docker

The compose files mount this folder read-only into the container at `/app/downloads`. Keep the large zip **on the host** — it is gitignored and not copied into the image.
