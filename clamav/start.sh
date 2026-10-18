#!/bin/sh
# Match the site's 100 MB ClamAV cap. Larger clients/patches skip the scan.
set -e
CONF=/etc/clamav/clamd.conf
if [ -f "$CONF" ]; then
	set_opt() {
		key=$1
		val=$2
		if grep -E "^#?${key} " "$CONF" >/dev/null 2>&1; then
			sed -i "s/^#\{0,1\}${key} .*/${key} ${val}/" "$CONF"
		else
			echo "${key} ${val}" >> "$CONF"
		fi
	}
	set_opt StreamMaxLength 100M
	set_opt MaxFileSize 100M
	set_opt MaxScanSize 100M
	set_opt ReadTimeout 600
	set_opt ConcurrentDatabaseReload no
fi
exec /init
