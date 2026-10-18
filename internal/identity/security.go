package identity

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func addColumn(db *sql.DB, table, name, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, nn, pk int
		var n, typ string
		var def any
		if err := rows.Scan(&cid, &n, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || n == name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition)
	return err
}
func migrateSecurity(db *sql.DB) error {
	for _, c := range [][3]string{{"users", "auth_version", "INTEGER NOT NULL DEFAULT 0"}, {"users", "auth_changed_at", "INTEGER NOT NULL DEFAULT 0"}, {"users", "dek_wrap_version", "INTEGER NOT NULL DEFAULT 0"}, {"users", "secrets_need_entry", "INTEGER NOT NULL DEFAULT 0"}, {"user_totp", "last_step", "INTEGER NOT NULL DEFAULT -1"}} {
		if err := addColumn(db, c[0], c[1], c[2]); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS totp_pending(user_id INTEGER PRIMARY KEY, secret TEXT NOT NULL, expires_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	if err := addColumn(db, "totp_pending", "auth_version", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	var marker string
	err := db.QueryRow(`SELECT v FROM identity_meta WHERE k='security_v2'`).Scan(&marker)
	if err == nil && marker == "complete" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := db.Exec(`PRAGMA secure_delete=ON`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if marker != "compacting" {
		type row struct {
			id          uint32
			hash        string
			wrap, nonce []byte
			version     int
		}
		rows, err := tx.Query(`SELECT id,password_hash,dek_wrap,dek_wrap_nonce,dek_wrap_version FROM users`)
		if err != nil {
			return err
		}
		var all []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.hash, &r.wrap, &r.nonce, &r.version); err != nil {
				rows.Close()
				return err
			}
			all = append(all, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, r := range all {
			if NeedsLegacy(r.hash) {
				continue
			}
			rec, err := parsePassword(r.hash)
			if err != nil {
				return fmt.Errorf("credential migration user %d: invalid record", r.id)
			}
			if r.version != 0 && r.version != 2 {
				return fmt.Errorf("unsupported DEK wrap version for user %d", r.id)
			}
			if !rec.legacy {
				continue
			}
			hash := encodePassword(rec.salt, derive(rec.verifier, "password-verifier"))
			var wrap, nonce []byte
			lost := 0
			if len(r.wrap) > 0 || len(r.nonce) > 0 {
				dek, err := open(rec.verifier, r.nonce, r.wrap)
				if err != nil || len(dek) != 32 {
					lost = 1
					if _, err = tx.Exec(`UPDATE wow_links SET secret_blob=NULL,secret_nonce=NULL WHERE user_id=?`, r.id); err != nil {
						return err
					}
				} else {
					nonce, wrap, err = sealAAD(derive(rec.verifier, "dek-wrap"), dek, wrapAAD(r.id))
					clear(dek)
					if err != nil {
						return err
					}
				}
			} else {
				res, err := tx.Exec(`UPDATE wow_links SET secret_blob=NULL,secret_nonce=NULL WHERE user_id=? AND (secret_blob IS NOT NULL OR secret_nonce IS NOT NULL)`, r.id)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				if n > 0 {
					lost = 1
				}
			}
			if _, err = tx.Exec(`UPDATE users SET password_hash=?,dek_wrap=?,dek_wrap_nonce=?,dek_wrap_version=2,secrets_need_entry=? WHERE id=?`, hash, wrap, nonce, lost, r.id); err != nil {
				return err
			}
		}
		// Pending registration may not exist when Store is used outside the web server.
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='pending_accounts'`).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			rows, err := tx.Query(`SELECT token_hash,password_hash FROM pending_accounts`)
			if err != nil {
				return err
			}
			hashes := map[string]string{}
			for rows.Next() {
				var token, h string
				if err := rows.Scan(&token, &h); err != nil {
					rows.Close()
					return err
				}
				hashes[token] = h
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for token, h := range hashes {
				if NeedsLegacy(h) {
					continue
				}
				r, err := parsePassword(h)
				if err != nil {
					return fmt.Errorf("invalid pending password record")
				}
				if r.legacy {
					if _, err = tx.Exec(`UPDATE pending_accounts SET password_hash=? WHERE token_hash=?`, encodePassword(r.salt, derive(r.verifier, "password-verifier")), token); err != nil {
						return err
					}
				}
			}
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS http_sessions; DELETE FROM user_totp WHERE enabled=0; UPDATE users SET auth_version=auth_version+1; INSERT INTO identity_meta(k,v) VALUES('security_v2','compacting') ON CONFLICT(k) DO UPDATE SET v=excluded.v`); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Retry compaction after interruption before accepting traffic.
	if err := checkpoint(db); err != nil {
		return err
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return err
	}
	if err := checkpoint(db); err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE identity_meta SET v='complete' WHERE k='security_v2'`)
	return err
}
func checkpoint(db *sql.DB) error {
	var busy, log, done int
	if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("security migration requires exclusive database access")
	}
	return nil
}

func (s *Store) ReplacePassword(ctx context.Context, id uint32, old, hash string, wrap, nonce []byte, reset bool, version uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?,dek_wrap=?,dek_wrap_nonce=?,dek_wrap_version=2,secrets_need_entry=?,auth_version=auth_version+1,auth_changed_at=? WHERE id=? AND password_hash=? AND auth_version=? AND auth_mode='local'`, hash, wrap, nonce, reset, time.Now().UnixNano(), id, old, version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBadPassword
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM totp_pending WHERE user_id=?`, id); err != nil {
		return err
	}
	if reset {
		if _, err := tx.ExecContext(ctx, `UPDATE wow_links SET secret_blob=NULL,secret_nonce=NULL WHERE user_id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) BumpAuthVersion(ctx context.Context, id uint32) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET auth_version=auth_version+1,auth_changed_at=? WHERE id=?`, time.Now().UnixNano(), id)
	return err
}

// checkVersion fences credential mutations against concurrent resets and revocation.
func checkVersion(ctx context.Context, tx *sql.Tx, userID uint32, expected []uint64) error {
	if len(expected) == 0 {
		return nil
	}
	var current uint64
	if err := tx.QueryRowContext(ctx, "SELECT auth_version FROM users WHERE id=?", userID).Scan(&current); err != nil {
		return err
	}
	if current != expected[0] {
		return fmt.Errorf("authentication changed; sign in again")
	}
	return nil
}
