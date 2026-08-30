package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/srp6"
)

var (
	ErrBanned     = errors.New("account is suspended")
	ErrInvalidBan = errors.New("invalid ban")
)

type BanInfo struct {
	Reason string
	Until  string // "permanent" or UTC timestamp
}

func ParseBanDuration(code string) (seconds int64, soapDur, label string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "", "perm", "permanent", "-1":
		return 0, "-1", "permanent", true
	case "1h":
		return 3600, "1h", "1 hour", true
	case "1d":
		return 86400, "1d", "1 day", true
	case "7d":
		return 7 * 86400, "7d", "7 days", true
	case "30d":
		return 30 * 86400, "30d", "30 days", true
	default:
		return 0, "", "", false
	}
}

func SanitizeBanReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 255 {
		s = s[:255]
	}
	return s
}

func (s *Service) Ban(ctx context.Context, actorGM uint8, actor, target, durationCode, reason string) error {
	target = srp6.UpperLatin(strings.TrimSpace(target))
	actor = srp6.UpperLatin(strings.TrimSpace(actor))
	reason = SanitizeBanReason(reason)
	if target == "" {
		return ErrNotFound
	}
	if target == actor {
		return ErrForbidden
	}
	if len(reason) < 3 {
		return ErrInvalidBan
	}
	secs, soapDur, _, ok := ParseBanDuration(durationCode)
	if !ok {
		return ErrInvalidBan
	}
	lvl := s.accountGMLevel(ctx, target)
	if lvl > actorGM {
		return ErrForbidden
	}
	if s.cfg.DemoMode || s.db == nil {
		return s.banMem(target, secs, reason)
	}
	if _, err := s.lookupAccountID(ctx, target); err != nil {
		return err
	}
	if s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode {
		if err := s.soap.BanAccount(ctx, target, soapDur, reason); err == nil {
			return nil
		} else if s.createMode() == "soap" {
			return err
		}
	}
	return s.banSQL(ctx, actor, target, secs, reason)
}

func (s *Service) Unban(ctx context.Context, actorGM uint8, actor, target string) error {
	target = srp6.UpperLatin(strings.TrimSpace(target))
	actor = srp6.UpperLatin(strings.TrimSpace(actor))
	if target == "" {
		return ErrNotFound
	}
	if target == actor {
		return ErrForbidden
	}
	lvl := s.accountGMLevel(ctx, target)
	if lvl > actorGM {
		return ErrForbidden
	}
	if s.cfg.DemoMode || s.db == nil {
		return s.unbanMem(target)
	}
	if _, err := s.lookupAccountID(ctx, target); err != nil {
		return err
	}
	if s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode {
		if err := s.soap.UnbanAccount(ctx, target); err == nil {
			return nil
		} else if s.createMode() == "soap" {
			return err
		}
	}
	return s.unbanSQL(ctx, target)
}

func (s *Service) banMem(target string, seconds int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.mem[target]
	if !ok {
		return ErrNotFound
	}
	a.Banned = true
	a.BanReason = reason
	if seconds > 0 {
		a.BanUntil = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	} else {
		a.BanUntil = time.Time{}
	}
	return nil
}

func (s *Service) unbanMem(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.mem[target]
	if !ok {
		return ErrNotFound
	}
	a.Banned = false
	a.BanReason = ""
	a.BanUntil = time.Time{}
	return nil
}

func (s *Service) banSQL(ctx context.Context, actor, target string, seconds int64, reason string) error {
	id, err := s.lookupAccountID(ctx, target)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	unban := now
	if seconds > 0 {
		unban = now + seconds
	}
	tbl := s.db.QAuth("account_banned")
	_, _ = s.db.SQL.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET `active`=0 WHERE `id`=? AND `active`=1", tbl), id)
	q := fmt.Sprintf("INSERT INTO %s (`id`,`bandate`,`unbandate`,`bannedby`,`banreason`,`active`) VALUES (?,?,?,?,?,1)", tbl)
	_, err = s.db.SQL.ExecContext(ctx, q, id, now, unban, actor, reason)
	if err != nil {
		return fmt.Errorf("ban account: %w", err)
	}
	return nil
}

func (s *Service) unbanSQL(ctx context.Context, target string) error {
	id, err := s.lookupAccountID(ctx, target)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("UPDATE %s SET `active`=0 WHERE `id`=? AND `active`=1", s.db.QAuth("account_banned"))
	_, err = s.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("unban account: %w", err)
	}
	return nil
}

func (s *Service) IsBanned(ctx context.Context, username string) (bool, BanInfo) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		a, ok := s.mem[username]
		if !ok || !a.Banned {
			return false, BanInfo{}
		}
		if !a.BanUntil.IsZero() && time.Now().UTC().After(a.BanUntil) {
			a.Banned = false
			return false, BanInfo{}
		}
		return true, BanInfo{Reason: a.BanReason, Until: formatBanUntil(a.BanUntil)}
	}
	id, err := s.lookupAccountID(ctx, username)
	if err != nil {
		return false, BanInfo{}
	}
	info, ok := s.activeBan(ctx, id)
	return ok, info
}

func (s *Service) activeBan(ctx context.Context, id uint32) (BanInfo, bool) {
	q := fmt.Sprintf(`
		SELECT `+"`banreason`"+`, `+"`bandate`"+`, `+"`unbandate`"+`
		FROM %s
		WHERE `+"`id`"+`=? AND `+"`active`"+`=1
		  AND (`+"`unbandate`"+`=`+"`bandate`"+` OR `+"`unbandate`"+`=0 OR `+"`unbandate`"+` > UNIX_TIMESTAMP())
		ORDER BY `+"`bandate`"+` DESC LIMIT 1`, s.db.QAuth("account_banned"))
	var reason string
	var bandate, unbandate int64
	err := s.db.SQL.QueryRowContext(ctx, q, id).Scan(&reason, &bandate, &unbandate)
	if err != nil {
		return BanInfo{}, false
	}
	until := "permanent"
	if unbandate > 0 && unbandate != bandate {
		until = time.Unix(unbandate, 0).UTC().Format("2006-01-02 15:04")
	}
	return BanInfo{Reason: reason, Until: until}, true
}

func (s *Service) attachBans(ctx context.Context, rows []ListedAccount) {
	if len(rows) == 0 || s.cfg.DemoMode || s.db == nil {
		return
	}
	ids := make([]any, 0, len(rows))
	ph := make([]string, 0, len(rows))
	idx := map[uint32]int{}
	for i, r := range rows {
		ids = append(ids, r.ID)
		ph = append(ph, "?")
		idx[r.ID] = i
	}
	q := fmt.Sprintf(`
		SELECT `+"`id`"+`, `+"`banreason`"+`, `+"`bandate`"+`, `+"`unbandate`"+`
		FROM %s
		WHERE `+"`active`"+`=1 AND `+"`id`"+` IN (%s)
		  AND (`+"`unbandate`"+`=`+"`bandate`"+` OR `+"`unbandate`"+`=0 OR `+"`unbandate`"+` > UNIX_TIMESTAMP())`,
		s.db.QAuth("account_banned"), strings.Join(ph, ","))
	qrows, err := s.db.SQL.QueryContext(ctx, q, ids...)
	if err != nil {
		return
	}
	defer qrows.Close()
	for qrows.Next() {
		var id uint32
		var reason string
		var bandate, unbandate int64
		if err := qrows.Scan(&id, &reason, &bandate, &unbandate); err != nil {
			return
		}
		i, ok := idx[id]
		if !ok {
			continue
		}
		rows[i].Banned = true
		rows[i].BanReason = reason
		rows[i].BanUntil = "permanent"
		if unbandate > 0 && unbandate != bandate {
			rows[i].BanUntil = time.Unix(unbandate, 0).UTC().Format("2006-01-02 15:04")
		}
	}
}

func formatBanUntil(t time.Time) string {
	if t.IsZero() {
		return "permanent"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func (a ListedAccount) StatusLabel() string {
	if a.Banned {
		return "suspended"
	}
	if a.Online {
		return "online"
	}
	return "offline"
}
