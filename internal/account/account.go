package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeroward/waygate/internal/config"
	"github.com/zeroward/waygate/internal/db"
	"github.com/zeroward/waygate/internal/soap"
	"github.com/zeroward/waygate/internal/srp6"
)

var (
	ErrNotFound      = errors.New("account not found")
	ErrTaken         = errors.New("username is already taken")
	ErrEmailTaken    = errors.New("email is already registered")
	ErrBadPassword   = errors.New("invalid username or password")
	ErrUnavailable   = errors.New("account service unavailable")
	ErrResetDisabled = errors.New("password reset is not configured")
	ErrResetToken    = errors.New("reset link is invalid or expired")
	ErrForbidden     = errors.New("you cannot modify that account")
	ErrBadRank       = errors.New("that rank cannot be granted")
)

const (
	RankPlayer  uint8 = 0
	RankMod     uint8 = 1
	RankGM      uint8 = 2
	RankAdmin   uint8 = 3
	RankSuperGM uint8 = 4 // console; never granted from the website
)

func RankName(level uint8) string {
	switch level {
	case RankMod:
		return "Moderator"
	case RankGM:
		return "GM"
	case RankAdmin:
		return "Admin"
	case RankSuperGM:
		return "Super GM"
	default:
		return "Player"
	}
}

func CanGrantRank(actorGM, targetGM, newLevel uint8) error {
	if newLevel != RankPlayer && newLevel != RankGM && newLevel != RankAdmin {
		return ErrBadRank
	}
	if targetGM > actorGM {
		return ErrForbidden
	}
	if newLevel >= actorGM {
		return ErrBadRank
	}
	return nil
}

type Account struct {
	ID       uint32
	Username string
	Email    string
	GMLevel  uint8
}

type ListedAccount struct {
	ID        uint32
	Username  string
	Email     string
	JoinDate  string
	LastLogin string
	LastIP    string
	Online    bool
	Expansion uint8
	GMLevel   uint8
	Locked    bool
	Banned    bool
	BanReason string
	BanUntil  string
	Gatehouse string // website username when this Wow.exe login is linked
	Linked    []string
}

type Service struct {
	cfg  config.Config
	db   *db.DB
	soap *soap.Client

	mu    sync.Mutex
	mem   map[string]*memAccount // demo / in-memory
	reset map[string]resetRec
}

type memAccount struct {
	ID        uint32
	Username  string
	Email     string
	Salt      []byte
	Verifier  []byte
	GMLevel   uint8
	Joined    time.Time
	Banned    bool
	BanReason string
	BanUntil  time.Time
}

type resetRec struct {
	Username string
	Expiry   time.Time
	Used     bool
}

func New(cfg config.Config, database *db.DB, soapc *soap.Client) *Service {
	return &Service{
		cfg:   cfg,
		db:    database,
		soap:  soapc,
		mem:   make(map[string]*memAccount),
		reset: make(map[string]resetRec),
	}
}

func SignupVerifier(username, password string) (salt, verifier []byte, err error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	return srp6.MakeRegistrationData(username, password)
}

func (s *Service) Create(ctx context.Context, username, password, email string, expansion uint8) error {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	email = strings.TrimSpace(email)

	taken, err := s.UsernameTaken(ctx, username)
	if err != nil {
		return err
	}
	if taken {
		return ErrTaken
	}
	if s.cfg.RequireUniqueEmail {
		et, err := s.EmailTaken(ctx, email)
		if err != nil {
			return err
		}
		if et {
			return ErrEmailTaken
		}
	}

	mode := s.createMode()
	err = nil
	switch mode {
	case "soap":
		err = s.createSOAP(ctx, username, password, email, expansion)
	case "sql":
		err = s.createSQL(ctx, username, password, email, expansion)
	default: // auto
		if s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode {
			err = s.createSOAP(ctx, username, password, email, expansion)
			if errors.Is(err, ErrTaken) {
				return err
			}
			if err != nil && s.db != nil {
				err = s.createSQL(ctx, username, password, email, expansion)
			}
		} else {
			err = s.createSQL(ctx, username, password, email, expansion)
		}
	}
	if err != nil {
		return err
	}
	if _, err := s.waitForAccount(ctx, username); err != nil {
		if mode != "soap" && s.db != nil && !s.cfg.DemoMode {
			if sqlErr := s.createSQL(ctx, username, password, email, expansion); sqlErr == nil {
				_, err = s.waitForAccount(ctx, username)
			} else if !errors.Is(sqlErr, ErrTaken) {
				return sqlErr
			} else {
				_, err = s.waitForAccount(ctx, username)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) waitForAccount(ctx context.Context, username string) (uint32, error) {
	var last error
	for i := 0; i < 10; i++ {
		id, err := s.lookupAccountID(ctx, username)
		if err == nil {
			return id, nil
		}
		last = err
		if errors.Is(err, ErrNotFound) && i < 9 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
			continue
		}
		return 0, err
	}
	if last == nil {
		last = ErrNotFound
	}
	return 0, last
}

func (s *Service) createMode() string {
	if s.cfg.DemoMode {
		return "sql" // memory path inside createSQL
	}
	return s.cfg.AccountMode
}

func (s *Service) createSOAP(ctx context.Context, username, password, email string, expansion uint8) error {
	if s.soap == nil {
		return ErrUnavailable
	}
	if err := s.soap.CreateAccount(ctx, username, password, email); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "taken") {
			return ErrTaken
		}
		return err
	}
	_ = s.soap.SetAddon(ctx, username, expansion)
	if email != "" {
		_ = s.soap.SetEmail(ctx, username, email)
	}
	if s.db != nil {
		_ = s.updateEmailExpansion(ctx, username, email, expansion)
	}
	return nil
}

func (s *Service) createSQL(ctx context.Context, username, password, email string, expansion uint8) error {
	salt, verifier, err := srp6.MakeRegistrationData(username, password)
	if err != nil {
		return ErrUnavailable
	}
	return s.insertPrepared(ctx, username, email, expansion, salt, verifier)
}

// CreatePrepared inserts an account from already-computed SRP6 material (email verify).
func (s *Service) CreatePrepared(ctx context.Context, username, email string, expansion uint8, salt, verifier []byte) error {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	email = strings.TrimSpace(email)
	if username == "" || len(salt) == 0 || len(verifier) == 0 {
		return ErrUnavailable
	}
	taken, err := s.UsernameTaken(ctx, username)
	if err != nil {
		return err
	}
	if taken {
		return ErrTaken
	}
	if s.cfg.RequireUniqueEmail && email != "" {
		et, err := s.EmailTaken(ctx, email)
		if err != nil {
			return err
		}
		if et {
			return ErrEmailTaken
		}
	}
	return s.insertPrepared(ctx, username, email, expansion, salt, verifier)
}

func (s *Service) insertPrepared(ctx context.Context, username, email string, expansion uint8, salt, verifier []byte) error {
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.mem[username]; ok {
			return ErrTaken
		}
		s.mem[username] = &memAccount{
			ID:       uint32(len(s.mem) + 1),
			Username: username,
			Email:    email,
			Salt:     salt,
			Verifier: verifier,
			Joined:   time.Now().UTC(),
		}
		return nil
	}
	q := fmt.Sprintf(
		"INSERT INTO %s (`username`,`salt`,`verifier`,`email`,`reg_mail`,`expansion`) VALUES (?,?,?,?,?,?)",
		s.db.QAuth("account"),
	)
	_, err := s.db.SQL.ExecContext(ctx, q, username, salt, verifier, email, email, expansion)
	if err != nil {
		if isDup(err) {
			return ErrTaken
		}
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, username, password string) (*Account, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		a, ok := s.mem[username]
		if !ok || !srp6.CheckLogin(username, password, a.Salt, a.Verifier) {
			return nil, ErrBadPassword
		}
		if a.Banned {
			if !a.BanUntil.IsZero() && time.Now().UTC().After(a.BanUntil) {
				a.Banned = false
			} else {
				return nil, ErrBanned
			}
		}
		return &Account{ID: a.ID, Username: a.Username, Email: a.Email, GMLevel: a.GMLevel}, nil
	}
	q := fmt.Sprintf("SELECT `id`,`username`,`email`,`salt`,`verifier` FROM %s WHERE `username`=? LIMIT 1", s.db.QAuth("account"))
	var (
		id       uint32
		user     string
		email    string
		salt     []byte
		verifier []byte
	)
	err := s.db.SQL.QueryRowContext(ctx, q, username).Scan(&id, &user, &email, &salt, &verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBadPassword
	}
	if err != nil {
		return nil, err
	}
	if !srp6.CheckLogin(user, password, salt, verifier) {
		return nil, ErrBadPassword
	}
	if _, ok := s.activeBan(ctx, id); ok {
		return nil, ErrBanned
	}
	return &Account{ID: id, Username: user, Email: email, GMLevel: s.lookupGMLevel(ctx, id)}, nil
}

func (s *Service) lookupGMLevel(ctx context.Context, accountID uint32) uint8 {
	if s.db == nil {
		return 0
	}
	q := fmt.Sprintf("SELECT COALESCE(MAX(`gmlevel`), 0) FROM %s WHERE `id`=?", s.db.QAuth("account_access"))
	var lvl uint8
	if err := s.db.SQL.QueryRowContext(ctx, q, accountID).Scan(&lvl); err != nil {
		return 0
	}
	return lvl
}

func (s *Service) GrantGM(username string, level uint8) {
	username = srp6.UpperLatin(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.mem[username]; ok {
		a.GMLevel = level
	}
}

func (s *Service) SetGMLevel(ctx context.Context, actorGM uint8, actorUser, target string, level uint8) error {
	target = srp6.UpperLatin(strings.TrimSpace(target))
	actorUser = srp6.UpperLatin(strings.TrimSpace(actorUser))
	if target == "" {
		return ErrNotFound
	}
	if target == actorUser {
		return ErrForbidden
	}
	current := s.accountGMLevel(ctx, target)
	if err := CanGrantRank(actorGM, current, level); err != nil {
		return err
	}

	return s.ApplyGMLevel(ctx, target, level)
}

// ApplyGMLevel writes account_access for a Wow.exe login. Never grants Super GM (4).
func (s *Service) ApplyGMLevel(ctx context.Context, username string, level uint8) error {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if username == "" {
		return ErrNotFound
	}
	if level > RankAdmin {
		return ErrBadRank
	}
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		a, ok := s.mem[username]
		if !ok {
			return ErrNotFound
		}
		a.GMLevel = level
		return nil
	}

	if _, err := s.lookupAccountID(ctx, username); err != nil {
		return err
	}

	if s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode {
		if err := s.soap.SetGMLevel(ctx, username, level); err == nil {
			return nil
		} else if s.createMode() == "soap" {
			return err
		}
	}
	return s.setGMLevelSQL(ctx, username, level)
}

func (s *Service) lookupAccountID(ctx context.Context, username string) (uint32, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if a, ok := s.mem[username]; ok {
			return a.ID, nil
		}
		return 0, ErrNotFound
	}
	q := fmt.Sprintf("SELECT `id` FROM %s WHERE `username`=? LIMIT 1", s.db.QAuth("account"))
	var id uint32
	err := s.db.SQL.QueryRowContext(ctx, q, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) setGMLevelSQL(ctx context.Context, username string, level uint8) error {
	id, err := s.lookupAccountID(ctx, username)
	if err != nil {
		return err
	}
	del := fmt.Sprintf("DELETE FROM %s WHERE `id`=?", s.db.QAuth("account_access"))
	if _, err := s.db.SQL.ExecContext(ctx, del, id); err != nil {
		return fmt.Errorf("clear gm access: %w", err)
	}
	if level == 0 {
		return nil
	}
	ins := fmt.Sprintf("INSERT INTO %s (`id`,`gmlevel`,`RealmID`) VALUES (?,?,?)", s.db.QAuth("account_access"))
	if _, err := s.db.SQL.ExecContext(ctx, ins, id, level, -1); err != nil {
		return fmt.Errorf("set gm access: %w", err)
	}
	return nil
}

func (s *Service) ResetPasswordByGM(ctx context.Context, actorGM uint8, target, newPassword string) error {
	target = srp6.UpperLatin(strings.TrimSpace(target))
	lvl := s.accountGMLevel(ctx, target)
	if lvl > actorGM {
		return ErrForbidden
	}
	return s.ResetPassword(ctx, target, newPassword)
}

func (s *Service) accountGMLevel(ctx context.Context, username string) uint8 {
	username = srp6.UpperLatin(username)
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if a, ok := s.mem[username]; ok {
			return a.GMLevel
		}
		return 0
	}
	q := fmt.Sprintf(
		"SELECT COALESCE(MAX(aa.`gmlevel`), 0) FROM %s a LEFT JOIN %s aa ON aa.`id` = a.`id` WHERE a.`username`=?",
		s.db.QAuth("account"), s.db.QAuth("account_access"),
	)
	var lvl uint8
	if err := s.db.SQL.QueryRowContext(ctx, q, username).Scan(&lvl); err != nil {
		return 0
	}
	return lvl
}

type ListFilter struct {
	Query       string
	IncludeBots bool
	Limit       int
	Offset      int
}

func (s *Service) ListAccounts(ctx context.Context, f ListFilter) ([]ListedAccount, int, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 40
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if s.cfg.DemoMode || s.db == nil {
		return s.listMem(f)
	}
	return s.listSQL(ctx, f)
}

func (s *Service) listMem(f ListFilter) ([]ListedAccount, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToUpper(strings.TrimSpace(f.Query))
	var all []ListedAccount
	for _, a := range s.mem {
		if !f.IncludeBots && isBotName(a.Username, s.cfg.BotPrefixes) {
			continue
		}
		if q != "" && !strings.Contains(a.Username, q) && !strings.Contains(strings.ToUpper(a.Email), q) {
			continue
		}
		banned := a.Banned && (a.BanUntil.IsZero() || !time.Now().UTC().After(a.BanUntil))
		row := ListedAccount{
			ID:        a.ID,
			Username:  a.Username,
			Email:     a.Email,
			JoinDate:  a.Joined.UTC().Format("2006-01-02 15:04"),
			LastLogin: "—",
			Expansion: 2,
			GMLevel:   a.GMLevel,
			Banned:    banned,
			BanReason: a.BanReason,
		}
		if banned {
			row.BanUntil = formatBanUntil(a.BanUntil)
		}
		all = append(all, row)
	}
	sortListed(all)
	total := len(all)
	if f.Offset > total {
		return nil, total, nil
	}
	end := f.Offset + f.Limit
	if end > total {
		end = total
	}
	return all[f.Offset:end], total, nil
}

func (s *Service) GetListed(ctx context.Context, username string) (ListedAccount, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if username == "" {
		return ListedAccount{}, ErrNotFound
	}
	if s.cfg.DemoMode || s.db == nil {
		rows, _, err := s.listMem(ListFilter{Query: username, IncludeBots: true, Limit: 100})
		if err != nil {
			return ListedAccount{}, err
		}
		for _, row := range rows {
			if row.Username == username {
				return row, nil
			}
		}
		return ListedAccount{}, ErrNotFound
	}
	q := fmt.Sprintf(`
		SELECT a.`+"`id`"+`, a.`+"`username`"+`, a.`+"`email`"+`, a.`+"`joindate`"+`,
		       CAST(a.`+"`last_login`"+` AS CHAR), a.`+"`last_ip`"+`, a.`+"`online`"+`, a.`+"`expansion`"+`, a.`+"`locked`"+`,
		       COALESCE(g.gmlevel, 0)
		FROM %s a
		LEFT JOIN (
			SELECT `+"`id`"+`, MAX(`+"`gmlevel`"+`) AS gmlevel FROM %s GROUP BY `+"`id`"+`
		) g ON g.`+"`id`"+` = a.`+"`id`"+`
		WHERE a.`+"`username`"+` = ?
		LIMIT 1`, s.db.QAuth("account"), s.db.QAuth("account_access"))
	var (
		row    ListedAccount
		join   time.Time
		last   sql.NullString
		online int
		locked int
		lastIP string
	)
	err := s.db.SQL.QueryRowContext(ctx, q, username).Scan(&row.ID, &row.Username, &row.Email, &join, &last, &lastIP, &online, &row.Expansion, &locked, &row.GMLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return ListedAccount{}, ErrNotFound
	}
	if err != nil {
		return ListedAccount{}, err
	}
	row.JoinDate = join.UTC().Format("2006-01-02 15:04")
	row.LastLogin = formatLastLogin(last)
	row.LastIP = lastIP
	row.Online = online != 0
	row.Locked = locked != 0
	out := []ListedAccount{row}
	s.attachBans(ctx, out)
	return out[0], nil
}

func formatLastLogin(last sql.NullString) string {
	if !last.Valid {
		return "—"
	}
	s := strings.TrimSpace(last.String)
	if s == "" || strings.HasPrefix(s, "0000-00-00") {
		return "—"
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	if len(s) >= 16 {
		return s[:16]
	}
	return s
}

func (s *Service) listSQL(ctx context.Context, f ListFilter) ([]ListedAccount, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if !f.IncludeBots {
		for _, p := range s.cfg.BotPrefixes {
			where = append(where, "a.`username` NOT LIKE ?")
			args = append(args, p+"%")
		}
	}
	q := sanitizeSearch(f.Query)
	if q != "" {
		where = append(where, "(a.`username` LIKE ? OR a.`email` LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	w := strings.Join(where, " AND ")
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM %s a WHERE %s", s.db.QAuth("account"), w)
	var total int
	if err := s.db.SQL.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listQ := fmt.Sprintf(`
		SELECT a.`+"`id`"+`, a.`+"`username`"+`, a.`+"`email`"+`, a.`+"`joindate`"+`,
		       CAST(a.`+"`last_login`"+` AS CHAR), a.`+"`last_ip`"+`, a.`+"`online`"+`, a.`+"`expansion`"+`, a.`+"`locked`"+`,
		       COALESCE(g.gmlevel, 0)
		FROM %s a
		LEFT JOIN (
			SELECT `+"`id`"+`, MAX(`+"`gmlevel`"+`) AS gmlevel FROM %s GROUP BY `+"`id`"+`
		) g ON g.`+"`id`"+` = a.`+"`id`"+`
		WHERE %s
		ORDER BY a.`+"`joindate`"+` DESC, a.`+"`id`"+` DESC
		LIMIT ? OFFSET ?`, s.db.QAuth("account"), s.db.QAuth("account_access"), w)
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.SQL.QueryContext(ctx, listQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ListedAccount
	for rows.Next() {
		var (
			row    ListedAccount
			join   time.Time
			last   sql.NullString
			online int
			locked int
			lastIP string
		)
		if err := rows.Scan(&row.ID, &row.Username, &row.Email, &join, &last, &lastIP, &online, &row.Expansion, &locked, &row.GMLevel); err != nil {
			return nil, 0, err
		}
		row.JoinDate = join.UTC().Format("2006-01-02 15:04")
		row.LastLogin = formatLastLogin(last)
		row.LastIP = lastIP
		row.Online = online != 0
		row.Locked = locked != 0
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	s.attachBans(ctx, out)
	return out, total, nil
}

func sanitizeSearch(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "%", "")
	s = strings.ReplaceAll(s, "_", "")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

func isBotName(username string, prefixes []string) bool {
	u := strings.ToUpper(username)
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

func sortListed(in []ListedAccount) {
	sort.Slice(in, func(i, j int) bool { return in[i].Username < in[j].Username })
}

func (s *Service) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	if _, err := s.Authenticate(ctx, username, oldPassword); err != nil {
		return err
	}
	username = srp6.UpperLatin(username)
	mode := s.createMode()
	if mode == "soap" || (mode == "auto" && s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode) {
		if err := s.soap.SetPassword(ctx, username, newPassword); err == nil {
			return nil
		} else if mode == "soap" {
			return err
		}
	}
	return s.updatePasswordSQL(ctx, username, newPassword)
}

func (s *Service) ResetPassword(ctx context.Context, username, newPassword string) error {
	username = srp6.UpperLatin(username)
	if s.soap != nil && s.cfg.SOAPConfigured() && !s.cfg.DemoMode {
		if err := s.soap.SetPassword(ctx, username, newPassword); err == nil {
			return nil
		}
	}
	return s.updatePasswordSQL(ctx, username, newPassword)
}

func (s *Service) updatePasswordSQL(ctx context.Context, username, newPassword string) error {
	salt, verifier, err := srp6.MakeRegistrationData(username, newPassword)
	if err != nil {
		return ErrUnavailable
	}
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		a, ok := s.mem[username]
		if !ok {
			return ErrNotFound
		}
		a.Salt = salt
		a.Verifier = verifier
		return nil
	}
	q := fmt.Sprintf("UPDATE %s SET `salt`=?, `verifier`=?, `session_key`=NULL WHERE `username`=?", s.db.QAuth("account"))
	res, err := s.db.SQL.ExecContext(ctx, q, salt, verifier, username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) UsernameTaken(ctx context.Context, username string) (bool, error) {
	username = srp6.UpperLatin(strings.TrimSpace(username))
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, ok := s.mem[username]
		return ok, nil
	}
	q := fmt.Sprintf("SELECT 1 FROM %s WHERE `username`=? LIMIT 1", s.db.QAuth("account"))
	var one int
	err := s.db.SQL.QueryRowContext(ctx, q, username).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) EmailTaken(ctx context.Context, email string) (bool, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return false, nil
	}
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, a := range s.mem {
			if strings.EqualFold(a.Email, email) {
				return true, nil
			}
		}
		return false, nil
	}
	q := fmt.Sprintf("SELECT 1 FROM %s WHERE `email`=? AND `email`<>'' LIMIT 1", s.db.QAuth("account"))
	var one int
	err := s.db.SQL.QueryRowContext(ctx, q, email).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) FindByEmail(ctx context.Context, email string) (*Account, error) {
	email = strings.TrimSpace(email)
	if s.cfg.DemoMode || s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, a := range s.mem {
			if strings.EqualFold(a.Email, email) {
				return &Account{ID: a.ID, Username: a.Username, Email: a.Email}, nil
			}
		}
		return nil, ErrNotFound
	}
	q := fmt.Sprintf("SELECT `id`,`username`,`email` FROM %s WHERE `email`=? LIMIT 1", s.db.QAuth("account"))
	var a Account
	err := s.db.SQL.QueryRowContext(ctx, q, email).Scan(&a.ID, &a.Username, &a.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) IssueResetToken(username string) (plain string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	plain = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	key := hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.reset[key] = resetRec{Username: srp6.UpperLatin(username), Expiry: time.Now().Add(15 * time.Minute)}
	s.mu.Unlock()
	return plain, nil
}

func (s *Service) ConsumeResetToken(token, newPassword string, ctx context.Context) error {
	user, err := s.ConsumeResetTokenUser(token)
	if err != nil {
		return err
	}
	return s.ResetPassword(ctx, user, newPassword)
}

func (s *Service) ConsumeResetTokenUser(token string) (string, error) {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.reset[key]
	if !ok || rec.Used || time.Now().After(rec.Expiry) {
		return "", ErrResetToken
	}
	rec.Used = true
	s.reset[key] = rec
	return rec.Username, nil
}

func (s *Service) updateEmailExpansion(ctx context.Context, username, email string, expansion uint8) error {
	if s.db == nil {
		return nil
	}
	q := fmt.Sprintf("UPDATE %s SET `email`=?, `reg_mail`=?, `expansion`=? WHERE `username`=?", s.db.QAuth("account"))
	_, err := s.db.SQL.ExecContext(ctx, q, email, email, expansion, username)
	return err
}

func isDup(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Duplicate") || strings.Contains(s, "1062")
}
