package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/downloads"
	"github.com/zeroward/waygate/internal/identity"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/md"
	"github.com/zeroward/waygate/internal/soap"
	"github.com/zeroward/waygate/internal/status"
	"github.com/zeroward/waygate/internal/validate"
)

const (
	adminActorDefault = "api"
	adminRankCeiling  = account.RankSuperGM
	adminJSONBodyMax  = 64 << 10
)

var adminSubActorRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

type adminEnvelope struct {
	Data any `json:"data"`
}

type adminAccountDTO struct {
	ID        uint32   `json:"id"`
	Username  string   `json:"username"`
	Email     string   `json:"email"`
	JoinDate  string   `json:"join_date"`
	LastLogin string   `json:"last_login"`
	LastIP    string   `json:"last_ip"`
	Online    bool     `json:"online"`
	Expansion uint8    `json:"expansion"`
	GMLevel   uint8    `json:"gm_level"`
	RankName  string   `json:"rank_name"`
	Locked    bool     `json:"locked"`
	Banned    bool     `json:"banned"`
	BanReason string   `json:"ban_reason,omitempty"`
	BanUntil  string   `json:"ban_until,omitempty"`
	Gatehouse string   `json:"gatehouse,omitempty"`
	Linked    []string `json:"linked"`
}

type adminIdentityDTO struct {
	ID         uint32 `json:"id"`
	Username   string `json:"username"`
	Email      string `json:"email"`
	StaffLevel uint8  `json:"staff_level"`
	RankName   string `json:"rank_name"`
	AuthMode   string `json:"auth_mode"`
	TOTPEnable bool   `json:"totp_enabled"`
	Passkeys   int    `json:"passkey_count"`
}

type adminCharacterDTO struct {
	GUID      uint32 `json:"guid"`
	AccountID uint32 `json:"account_id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Level     uint8  `json:"level"`
	Race      string `json:"race"`
	RaceID    uint8  `json:"race_id"`
	Class     string `json:"class"`
	ClassID   uint8  `json:"class_id"`
	Location  string `json:"location"`
	Faction   string `json:"faction"`
	Gold      string `json:"gold"`
	Played    string `json:"played"`
	Online    bool   `json:"online"`
}

type adminTicketDTO struct {
	ID            int64                 `json:"id"`
	PublicRef     string                `json:"public_ref"`
	AccountID     uint32                `json:"account_id"`
	Username      string                `json:"username"`
	Category      string                `json:"category"`
	Title         string                `json:"title"`
	CharacterGUID uint32                `json:"character_guid"`
	CharacterName string                `json:"character_name"`
	Status        string                `json:"status"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	ClosedBy      string                `json:"closed_by"`
	Messages      []adminTicketMsgDTO   `json:"messages,omitempty"`
}

type adminTicketMsgDTO struct {
	ID        int64     `json:"id"`
	TicketID  int64     `json:"ticket_id"`
	Author    string    `json:"author"`
	FromStaff bool      `json:"from_staff"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type adminArticleDTO struct {
	ID           int64     `json:"id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Category     string    `json:"category"`
	SortOrder    int       `json:"sort_order"`
	Published    bool      `json:"published"`
	CreatedBy    string    `json:"created_by"`
	UpdatedBy    string    `json:"updated_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	BodyMarkdown string    `json:"body_markdown,omitempty"`
	HTML         string    `json:"html,omitempty"`
}

type adminDownloadDTO struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Featured    bool   `json:"featured"`
	FileName    string `json:"file_name"`
	Size        int64  `json:"size_bytes"`
	SizeHuman   string `json:"size_human"`
	SHA256      string `json:"sha256"`
	Ready       bool   `json:"ready"`
	SourceURL   string `json:"source_url"`
	AddonID     string `json:"addon_id"`
	UploadedAt  string `json:"uploaded_at"`
	ClamAV      string `json:"clamav"`
	Mandatory   bool   `json:"mandatory"`
}

type adminCalendarEventDTO struct {
	ID        int64     `json:"id"`
	Date      string    `json:"date"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type adminAuditEventDTO struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
}

func (s *Server) adminAPI(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminAPIToken == "" {
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}
	token, ok := extractBearerToken(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		writeManagementError(w, r, http.StatusUnauthorized, "unauthorized", "A valid admin API bearer token is required.")
		return
	}
	if !matchBearerToken(s.cfg.AdminAPIToken, token) {
		if matchBearerToken(s.cfg.ManagementAPIToken, token) {
			writeManagementError(w, r, http.StatusForbidden, "insufficient_scope", "The read-only management token cannot access administrative endpoints.")
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		writeManagementError(w, r, http.StatusUnauthorized, "unauthorized", "A valid admin API bearer token is required.")
		return
	}

	actor, ok := resolveAdminActor(r)
	if !ok {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_actor", "X-Admin-Actor must be 1-32 alphanumeric, dot, underscore, or hyphen characters.")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
	if path == "" || path == "/" {
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}
	if strings.HasSuffix(path, "/") {
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}

	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch parts[0] {
	case "accounts":
		s.adminAccountsRoute(w, r, actor, parts[1:])
	case "banner":
		if len(parts) != 1 {
			writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
			return
		}
		s.adminBannerRoute(w, r, actor)
	case "register-key":
		if len(parts) != 1 {
			writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
			return
		}
		s.adminRegisterKeyRoute(w, r, actor)
	case "calendar":
		s.adminCalendarRoute(w, r, actor, parts[1:])
	case "audit":
		if len(parts) != 1 {
			writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
			return
		}
		s.adminAuditRoute(w, r)
	case "tickets":
		s.adminTicketsRoute(w, r, actor, parts[1:])
	case "kb":
		s.adminKBRoute(w, r, actor, parts[1:])
	case "downloads":
		s.adminDownloadsRoute(w, r, actor, parts[1:])
	case "game":
		s.adminGameRoute(w, r, actor, parts[1:])
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

func resolveAdminActor(r *http.Request) (string, bool) {
	sub := strings.TrimSpace(r.Header.Get("X-Admin-Actor"))
	if sub == "" {
		return adminActorDefault, true
	}
	if !adminSubActorRe.MatchString(sub) {
		return "", false
	}
	return adminActorDefault + ":" + sub, true
}

func writeAdminData(w http.ResponseWriter, r *http.Request, code int, data any) {
	writeManagementJSON(w, r, code, adminEnvelope{Data: data})
}

func readAdminJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	if maxBytes <= 0 {
		maxBytes = adminJSONBodyMax
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			writeManagementError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body exceeds size limit.")
			return false
		}
		writeManagementError(w, r, http.StatusBadRequest, "invalid_json", "Request body must be valid JSON matching the endpoint schema.")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_json", "Request body must contain a single JSON object.")
		return false
	}
	return true
}

func adminMethodNotAllowed(w http.ResponseWriter, r *http.Request, allow string) {
	w.Header().Set("Allow", allow)
	writeManagementError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed for this endpoint.")
}

func toAdminAccountDTO(a account.ListedAccount) adminAccountDTO {
	linked := a.Linked
	if linked == nil {
		linked = []string{}
	}
	return adminAccountDTO{
		ID:        a.ID,
		Username:  a.Username,
		Email:     a.Email,
		JoinDate:  a.JoinDate,
		LastLogin: a.LastLogin,
		LastIP:    a.LastIP,
		Online:    a.Online,
		Expansion: a.Expansion,
		GMLevel:   a.GMLevel,
		RankName:  account.RankName(a.GMLevel),
		Locked:    a.Locked,
		Banned:    a.Banned,
		BanReason: a.BanReason,
		BanUntil:  a.BanUntil,
		Gatehouse: a.Gatehouse,
		Linked:    linked,
	}
}

// --- Domain A: Accounts & Access ---

func (s *Server) adminAccountsRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if s.accounts == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "accounts_unavailable", "Account service is not available.")
		return
	}
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.adminAccountsList(w, r)
		case http.MethodPost:
			s.adminAccountsCreate(w, r, actor)
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, POST")
		}
	case 1:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			s.adminAccountsGet(w, r, rest[0])
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD")
		}
	case 2:
		username := rest[0]
		switch rest[1] {
		case "password":
			if r.Method != http.MethodPost {
				adminMethodNotAllowed(w, r, "POST")
				return
			}
			s.adminAccountsResetPassword(w, r, actor, username)
		case "rank":
			if r.Method != http.MethodPut {
				adminMethodNotAllowed(w, r, "PUT")
				return
			}
			s.adminAccountsSetRank(w, r, actor, username)
		case "ban":
			switch r.Method {
			case http.MethodPost:
				s.adminAccountsBan(w, r, actor, username)
			case http.MethodDelete:
				s.adminAccountsUnban(w, r, actor, username)
			default:
				adminMethodNotAllowed(w, r, "POST, DELETE")
			}
		default:
			writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		}
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

func (s *Server) adminAccountsList(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	botsRaw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("bots")))
	includeBots := botsRaw == "1" || botsRaw == "true"
	limit := 40
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "limit must be an integer between 1 and 100.")
			return
		}
		limit = n
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "offset must be a non-negative integer.")
			return
		}
		offset = n
	}
	rows, total, err := s.accounts.ListAccounts(r.Context(), account.ListFilter{
		Query:       q,
		IncludeBots: includeBots,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		s.log.Error("admin api accounts list", "err", err)
		writeManagementError(w, r, http.StatusServiceUnavailable, "accounts_unavailable", "Accounts could not be listed.")
		return
	}
	s.attachGatehouse(r.Context(), rows)
	out := make([]adminAccountDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAdminAccountDTO(row))
	}
	writeAdminData(w, r, http.StatusOK, map[string]any{
		"accounts": out,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

func (s *Server) adminAccountsGet(w http.ResponseWriter, r *http.Request, rawUser string) {
	target := strings.ToUpper(strings.TrimSpace(rawUser))
	if err := validate.Username(target); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}

	listed, err := s.accounts.GetListed(r.Context(), target)
	var idDTO *adminIdentityDTO
	var ownerID uint32
	var linkedNames []string

	if errors.Is(err, account.ErrNotFound) && s.id != nil {
		// Also support looking up by Gatehouse identity username if its primary WoW login differs.
		if u, uerr := s.id.GetByUsername(r.Context(), target); uerr == nil {
			ownerID = u.ID
			links, _ := s.id.Links(r.Context(), u.ID)
			for _, l := range links {
				linkedNames = append(linkedNames, l.Username)
			}
			if len(linkedNames) > 0 {
				listed, err = s.accounts.GetListed(r.Context(), linkedNames[0])
			}
		}
	}
	if errors.Is(err, account.ErrNotFound) {
		writeManagementError(w, r, http.StatusNotFound, "account_not_found", "Account not found.")
		return
	}
	if err != nil {
		s.log.Error("admin api account get", "err", err, "target", target)
		writeManagementError(w, r, http.StatusServiceUnavailable, "accounts_unavailable", "Account could not be loaded.")
		return
	}

	if s.id != nil {
		gh, uid, names := s.id.LinkedWowNames(r.Context(), listed.Username)
		if uid != 0 {
			ownerID = uid
		}
		if gh != "" {
			listed.Gatehouse = gh
		}
		if len(names) > 0 {
			listed.Linked = names
			linkedNames = names
		}
		if ownerID != 0 {
			if u, uerr := s.id.GetByID(r.Context(), ownerID); uerr == nil {
				totpOn, _ := s.id.TOTPEnabled(r.Context(), u.ID)
				pks, _ := s.id.Store().ListPasskeys(r.Context(), u.ID)
				idDTO = &adminIdentityDTO{
					ID:         u.ID,
					Username:   u.Username,
					Email:      u.Email,
					StaffLevel: u.StaffLevel,
					RankName:   account.RankName(u.StaffLevel),
					AuthMode:   u.AuthMode,
					TOTPEnable: totpOn,
					Passkeys:   len(pks),
				}
				if u.StaffLevel > listed.GMLevel && u.StaffLevel <= account.RankAdmin {
					listed.GMLevel = u.StaffLevel
				}
			}
		}
	}
	if len(linkedNames) == 0 {
		linkedNames = []string{listed.Username}
		listed.Linked = linkedNames
	}

	var chars []adminCharacterDTO
	if s.status != nil {
		ids := []uint32{listed.ID}
		if ownerID != 0 && s.id != nil {
			if accIDs, err := s.id.AccountIDs(r.Context(), ownerID); err == nil && len(accIDs) > 0 {
				ids = accIDs
			}
		}
		if list, err := s.status.AccountCharactersMany(r.Context(), ids); err == nil {
			chars = make([]adminCharacterDTO, 0, len(list))
			for _, ch := range list {
				chars = append(chars, adminCharacterDTO{
					GUID:      ch.GUID,
					AccountID: ch.AccountID,
					Login:     ch.Login,
					Name:      ch.Name,
					Level:     ch.Level,
					Race:      ch.Race,
					RaceID:    ch.RaceID,
					Class:     ch.Class,
					ClassID:   ch.ClassID,
					Location:  ch.Location,
					Faction:   ch.Faction,
					Gold:      ch.Gold,
					Played:    ch.Played,
					Online:    ch.Online,
				})
			}
		}
	}
	if chars == nil {
		chars = []adminCharacterDTO{}
	}

	writeAdminData(w, r, http.StatusOK, map[string]any{
		"account":       toAdminAccountDTO(listed),
		"identity":      idDTO,
		"linked_logins": linkedNames,
		"characters":    chars,
	})
}

func (s *Server) adminAccountsCreate(w http.ResponseWriter, r *http.Request, actor string) {
	var req struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Email     string `json:"email"`
		Expansion *uint8 `json:"expansion"`
	}
	if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
		return
	}
	user := strings.TrimSpace(req.Username)
	email := strings.TrimSpace(req.Email)
	pass := req.Password
	if err := validate.Username(user); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	minLen := s.cfg.PasswordMinLength
	if minLen < 8 {
		minLen = 8
	}
	if err := validate.Password(pass, user, minLen); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	if email != "" {
		if err := validate.Email(email); err != nil {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_email", err.Error())
			return
		}
	}
	exp := s.cfg.DefaultExpansion
	if req.Expansion != nil {
		if *req.Expansion > 2 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_expansion", "expansion must be 0, 1, or 2.")
			return
		}
		exp = *req.Expansion
	}

	var createErr error
	if s.id != nil {
		_, createErr = s.id.Register(r.Context(), user, pass, email, user, pass, exp)
	} else {
		createErr = s.accounts.Create(r.Context(), user, pass, email, exp)
	}
	if createErr != nil {
		switch {
		case errors.Is(createErr, identity.ErrTaken), errors.Is(createErr, account.ErrTaken):
			writeManagementError(w, r, http.StatusConflict, "username_taken", "Username is already taken.")
		case errors.Is(createErr, identity.ErrEmailTaken), errors.Is(createErr, account.ErrEmailTaken):
			writeManagementError(w, r, http.StatusConflict, "email_taken", "Email is already registered.")
		default:
			s.log.Error("admin api create account", "err", createErr, "actor", actor, "target", user)
			writeManagementError(w, r, http.StatusServiceUnavailable, "account_create_failed", "Could not create the account.")
		}
		return
	}

	upper := strings.ToUpper(user)
	s.log.Info("admin api create account", "actor", actor, "target", upper)
	s.logStaff(actor, "create", upper)

	listed, err := s.accounts.GetListed(r.Context(), upper)
	if err != nil {
		writeAdminData(w, r, http.StatusCreated, map[string]any{
			"username":  upper,
			"email":     email,
			"expansion": exp,
		})
		return
	}
	rows := []account.ListedAccount{listed}
	s.attachGatehouse(r.Context(), rows)
	writeAdminData(w, r, http.StatusCreated, toAdminAccountDTO(rows[0]))
}

func (s *Server) adminAccountsResetPassword(w http.ResponseWriter, r *http.Request, actor, rawUser string) {
	target := strings.TrimSpace(rawUser)
	if err := validate.Username(target); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	var req struct {
		Password      string `json:"password"`
		SyncGatehouse *bool  `json:"sync_gatehouse"`
	}
	if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
		return
	}
	minLen := s.cfg.PasswordMinLength
	if minLen < 8 {
		minLen = 8
	}
	if err := validate.Password(req.Password, target, minLen); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}
	if err := s.accounts.ResetPasswordByGM(r.Context(), adminRankCeiling, target, req.Password); err != nil {
		switch {
		case errors.Is(err, account.ErrNotFound):
			writeManagementError(w, r, http.StatusNotFound, "account_not_found", "Account not found.")
		case errors.Is(err, account.ErrForbidden):
			writeManagementError(w, r, http.StatusForbidden, "forbidden", "Cannot modify that account.")
		default:
			s.log.Error("admin api reset password", "err", err, "actor", actor, "target", target)
			writeManagementError(w, r, http.StatusServiceUnavailable, "password_reset_failed", "Could not reset password.")
		}
		return
	}

	syncGH := req.SyncGatehouse == nil || *req.SyncGatehouse
	ghUpdated := false
	if syncGH && s.id != nil {
		gh, userID, _ := s.linkedWowNames(r.Context(), target)
		if gh != "" {
			if err := s.id.SetPassword(r.Context(), gh, req.Password); err == nil {
				ghUpdated = true
			}
		}
		if userID != 0 && s.sessions != nil {
			s.sessions.RevokeUser(userID, "")
		}
	}

	upper := strings.ToUpper(target)
	s.log.Info("admin api reset password", "actor", actor, "target", upper)
	s.logStaff(actor, "reset", upper)
	writeAdminData(w, r, http.StatusOK, map[string]any{
		"username":          upper,
		"updated":           true,
		"gatehouse_updated": ghUpdated,
	})
}

func (s *Server) adminAccountsSetRank(w http.ResponseWriter, r *http.Request, actor, rawUser string) {
	target := strings.TrimSpace(rawUser)
	if err := validate.Username(target); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	var req struct {
		Rank *int `json:"rank"`
	}
	if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
		return
	}
	if req.Rank == nil || *req.Rank < 0 || *req.Rank > 255 {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_rank", "rank must be 0 (Player), 1 (Moderator), 2 (GM), or 3 (Admin).")
		return
	}
	level := uint8(*req.Rank)

	gh, userID, names := s.linkedWowNames(r.Context(), target)
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	// Check that the target account actually exists before modifying rank.
	if _, err := s.accounts.GetListed(r.Context(), names[0]); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeManagementError(w, r, http.StatusNotFound, "account_not_found", "Account not found.")
			return
		}
		writeManagementError(w, r, http.StatusServiceUnavailable, "accounts_unavailable", "Account could not be loaded.")
		return
	}

	current := s.accountsGM(r, names[0])
	for _, n := range names {
		if g := s.accountsGM(r, n); g > current {
			current = g
		}
	}
	if userID != 0 && s.id != nil {
		if u, err := s.id.GetByID(r.Context(), userID); err == nil && u.StaffLevel > current {
			current = u.StaffLevel
		}
	}
	if err := account.CanGrantRank(adminRankCeiling, current, level); err != nil {
		if errors.Is(err, account.ErrBadRank) {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_rank", "rank must be 0 (Player), 1 (Moderator), 2 (GM), or 3 (Admin); Super GM (4) cannot be granted via API.")
			return
		}
		writeManagementError(w, r, http.StatusForbidden, "forbidden", "Cannot modify that account.")
		return
	}

	if s.id != nil {
		if userID != 0 {
			if err := s.id.Store().SetStaffLevel(r.Context(), userID, level); err != nil {
				writeManagementError(w, r, http.StatusServiceUnavailable, "rank_update_failed", "Website role could not be updated.")
				return
			}
			if s.sessions != nil {
				s.sessions.RevokeUser(userID, "")
			}
		} else {
			if err := s.syncWebsiteStaffLevel(r.Context(), target, level); err != nil && !errors.Is(err, account.ErrNotFound) {
				writeManagementError(w, r, http.StatusServiceUnavailable, "rank_update_failed", "Website role could not be updated.")
				return
			}
		}
	}

	var last error
	applied := 0
	for _, n := range names {
		if g := s.accountsGM(r, n); g > account.RankAdmin {
			continue
		}
		if err := s.accounts.ApplyGMLevel(r.Context(), n, level); err != nil {
			last = err
			continue
		}
		applied++
	}
	if applied != len(names) {
		s.log.Error("admin api rank", "err", last, "actor", actor, "target", target, "rank", level)
		writeManagementError(w, r, http.StatusServiceUnavailable, "rank_update_failed", "Game role synchronization failed.")
		return
	}

	label := strings.ToUpper(target)
	if gh != "" {
		label = gh
	}
	s.log.Info("admin api rank", "actor", actor, "target", label, "rank", level, "logins", applied)
	s.logStaff(actor, "rank", label+"="+account.RankName(level))
	writeAdminData(w, r, http.StatusOK, map[string]any{
		"username":       strings.ToUpper(target),
		"gatehouse":      gh,
		"rank":           level,
		"rank_name":      account.RankName(level),
		"updated_logins": applied,
	})
}

func (s *Server) adminAccountsBan(w http.ResponseWriter, r *http.Request, actor, rawUser string) {
	target := strings.TrimSpace(rawUser)
	if err := validate.Username(target); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	var req struct {
		Duration string `json:"duration"`
		Reason   string `json:"reason"`
	}
	if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
		return
	}
	_, _, durLabel, ok := account.ParseBanDuration(req.Duration)
	if !ok {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_duration", "duration must be one of: 1h, 1d, 7d, 30d, permanent.")
		return
	}
	reason := account.SanitizeBanReason(req.Reason)
	if len(reason) < 3 {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_reason", "reason must be at least 3 characters.")
		return
	}

	gh, userID, names := s.linkedWowNames(r.Context(), target)
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	if userID != 0 && s.id != nil {
		if err := s.id.Store().BumpAuthVersion(r.Context(), userID); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "ban_failed", "Could not revoke authentication.")
			return
		}
		if s.sessions != nil {
			s.sessions.RevokeUser(userID, "")
		}
	}

	var last error
	banned := 0
	for _, n := range names {
		if err := s.accounts.Ban(r.Context(), adminRankCeiling, actor, n, req.Duration, reason); err != nil {
			last = err
			continue
		}
		banned++
	}
	if banned == 0 {
		switch {
		case errors.Is(last, account.ErrNotFound):
			writeManagementError(w, r, http.StatusNotFound, "account_not_found", "Account not found.")
		case errors.Is(last, account.ErrForbidden):
			writeManagementError(w, r, http.StatusForbidden, "forbidden", "Cannot suspend that account.")
		case errors.Is(last, account.ErrInvalidBan):
			writeManagementError(w, r, http.StatusBadRequest, "invalid_ban", "Invalid ban duration or reason.")
		default:
			s.log.Error("admin api ban", "err", last, "actor", actor, "target", target)
			writeManagementError(w, r, http.StatusServiceUnavailable, "ban_failed", "Could not suspend the account.")
		}
		return
	}
	if userID != 0 && s.sessions != nil {
		s.sessions.RevokeUser(userID, "")
	}
	if banned != len(names) {
		writeManagementError(w, r, http.StatusServiceUnavailable, "partial_ban", "Only some linked game logins were suspended; retry to finish.")
		return
	}

	who := strings.ToUpper(target)
	if gh != "" {
		who = gh
	}
	s.log.Info("admin api ban", "actor", actor, "target", who, "duration", durLabel, "logins", banned)
	s.logStaff(actor, "ban", who+"="+durLabel)
	writeAdminData(w, r, http.StatusOK, map[string]any{
		"target":           who,
		"duration":         durLabel,
		"reason":           reason,
		"suspended_logins": banned,
	})
}

func (s *Server) adminAccountsUnban(w http.ResponseWriter, r *http.Request, actor, rawUser string) {
	target := strings.TrimSpace(rawUser)
	if err := validate.Username(target); err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_username", err.Error())
		return
	}
	gh, _, names := s.linkedWowNames(r.Context(), target)
	if len(names) == 0 {
		names = []string{strings.ToUpper(target)}
	}
	var last error
	unbanned := 0
	for _, n := range names {
		if err := s.accounts.Unban(r.Context(), adminRankCeiling, actor, n); err != nil {
			last = err
			continue
		}
		unbanned++
	}
	if unbanned == 0 {
		switch {
		case errors.Is(last, account.ErrNotFound):
			writeManagementError(w, r, http.StatusNotFound, "account_not_found", "Account not found.")
		case errors.Is(last, account.ErrForbidden):
			writeManagementError(w, r, http.StatusForbidden, "forbidden", "Cannot modify that account.")
		default:
			s.log.Error("admin api unban", "err", last, "actor", actor, "target", target)
			writeManagementError(w, r, http.StatusServiceUnavailable, "unban_failed", "Could not lift the suspension.")
		}
		return
	}
	who := strings.ToUpper(target)
	if gh != "" {
		who = gh
	}
	s.log.Info("admin api unban", "actor", actor, "target", who, "logins", unbanned)
	s.logStaff(actor, "unban", who)
	writeAdminData(w, r, http.StatusOK, map[string]any{
		"target":          who,
		"unbanned_logins": unbanned,
	})
}

// --- Domain B: Portal Operations ---

func (s *Server) adminBannerState(r *http.Request) map[string]any {
	until := ""
	if s.kb != nil {
		v, _ := s.kb.GetSetting(r.Context(), kb.SettingMaintenanceUntil)
		until = strings.TrimSpace(v)
	}
	return map[string]any{
		"active":  s.maintenanceBanner(r.Context()),
		"message": s.bannerMessage(r.Context()),
		"until":   until,
	}
}

func (s *Server) adminBannerRoute(w http.ResponseWriter, r *http.Request, actor string) {
	if s.kb == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Settings storage is not available.")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeAdminData(w, r, http.StatusOK, s.adminBannerState(r))
	case http.MethodPut:
		var req struct {
			Message string `json:"message"`
			Until   string `json:"until"`
		}
		if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
			return
		}
		msg := kb.ClipBanner(req.Message)
		untilRaw := strings.TrimSpace(req.Until)
		until := ""
		if untilRaw != "" {
			parsed, err := parseBannerUntil(untilRaw)
			if err != nil {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_until", "until must be an RFC3339 timestamp or YYYY-MM-DDTHH:MM in UTC.")
				return
			}
			until = parsed
		}
		if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceMessage, msg); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "banner_save_failed", "Could not save the banner.")
			return
		}
		if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceUntil, until); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "banner_save_failed", "Could not save the banner.")
			return
		}
		if msg == "" {
			s.logStaff(actor, "banner-clear", "")
		} else {
			s.logStaff(actor, "banner-set", msg)
		}
		writeAdminData(w, r, http.StatusOK, s.adminBannerState(r))
	case http.MethodDelete:
		if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceMessage, ""); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "banner_save_failed", "Could not clear the banner.")
			return
		}
		if err := s.kb.SetSetting(r.Context(), kb.SettingMaintenanceUntil, ""); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "banner_save_failed", "Could not clear the banner.")
			return
		}
		s.logStaff(actor, "banner-clear", "")
		writeAdminData(w, r, http.StatusOK, s.adminBannerState(r))
	default:
		adminMethodNotAllowed(w, r, "GET, HEAD, PUT, DELETE")
	}
}

func (s *Server) adminRegisterKeyRoute(w http.ResponseWriter, r *http.Request, actor string) {
	if s.id == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Identity storage is not available.")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		k := s.registerKey()
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"required":     k != "",
			"register_key": k,
		})
	case http.MethodPut:
		var req struct {
			RegisterKey string `json:"register_key"`
		}
		if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
			return
		}
		key := strings.TrimSpace(req.RegisterKey)
		if err := validRegisterKey(key); err != nil {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_register_key", err.Error())
			return
		}
		if err := s.id.Store().SetRegisterKey(r.Context(), key); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "register_key_save_failed", "Could not save the registration key.")
			return
		}
		if key == "" {
			s.logStaff(actor, "register-key", "open")
		} else {
			s.logStaff(actor, "register-key", "set")
		}
		k := s.registerKey()
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"required":     k != "",
			"register_key": k,
		})
	case http.MethodDelete:
		if err := s.id.Store().SetRegisterKey(r.Context(), ""); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "register_key_save_failed", "Could not clear the registration key.")
			return
		}
		s.logStaff(actor, "register-key", "open")
		k := s.registerKey()
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"required":     k != "",
			"register_key": k,
		})
	default:
		adminMethodNotAllowed(w, r, "GET, HEAD, PUT, DELETE")
	}
}

func (s *Server) adminCalendarRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if s.kb == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Calendar storage is not available.")
		return
	}
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			limit := 20
			if v := r.URL.Query().Get("limit"); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 50 {
					writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "limit must be an integer between 1 and 50.")
					return
				}
				limit = n
			}
			list, err := s.kb.ListStaffEvents(r.Context(), limit)
			if err != nil {
				writeManagementError(w, r, http.StatusServiceUnavailable, "calendar_unavailable", "Could not load calendar events.")
				return
			}
			out := make([]adminCalendarEventDTO, 0, len(list))
			for _, ev := range list {
				out = append(out, adminCalendarEventDTO{
					ID:        ev.ID,
					Date:      ev.Date,
					Title:     ev.Title,
					Detail:    ev.Detail,
					CreatedBy: ev.CreatedBy,
					CreatedAt: ev.CreatedAt.UTC(),
				})
			}
			writeAdminData(w, r, http.StatusOK, map[string]any{"events": out})
		case http.MethodPost:
			var req struct {
				Date   string `json:"date"`
				Title  string `json:"title"`
				Detail string `json:"detail"`
			}
			if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
				return
			}
			date := strings.TrimSpace(req.Date)
			title := kb.ClipEventTitle(req.Title)
			detail := kb.ClipEventDetail(req.Detail)
			if !kb.ValidEventDate(date) {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_date", "date must be in YYYY-MM-DD format.")
				return
			}
			if title == "" {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_title", "title is required.")
				return
			}
			ev, err := s.kb.CreateEvent(r.Context(), date, title, detail, actor)
			if err != nil {
				writeManagementError(w, r, http.StatusServiceUnavailable, "calendar_save_failed", "Could not create the calendar event.")
				return
			}
			s.logStaff(actor, "event-add", ev.Date+" "+ev.Title)
			writeAdminData(w, r, http.StatusCreated, adminCalendarEventDTO{
				ID:        ev.ID,
				Date:      ev.Date,
				Title:     ev.Title,
				Detail:    ev.Detail,
				CreatedBy: ev.CreatedBy,
				CreatedAt: ev.CreatedAt.UTC(),
			})
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, POST")
		}
	case 1:
		if r.Method != http.MethodDelete {
			adminMethodNotAllowed(w, r, "DELETE")
			return
		}
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil || id < 1 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_id", "Invalid calendar event ID.")
			return
		}
		if err := s.kb.DeleteEvent(r.Context(), id); err != nil {
			if errors.Is(err, kb.ErrNotFound) {
				writeManagementError(w, r, http.StatusNotFound, "event_not_found", "Calendar event not found.")
				return
			}
			writeManagementError(w, r, http.StatusServiceUnavailable, "calendar_delete_failed", "Could not delete the calendar event.")
			return
		}
		s.logStaff(actor, "event-delete", rest[0])
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"deleted": true,
			"id":      id,
		})
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

func (s *Server) adminAuditRoute(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Audit storage is not available.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		adminMethodNotAllowed(w, r, "GET, HEAD")
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "limit must be an integer between 1 and 200.")
			return
		}
		limit = n
	}
	events, err := s.kb.RecentEvents(r.Context(), limit)
	if err != nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "audit_unavailable", "Could not load audit events.")
		return
	}
	out := make([]adminAuditEventDTO, 0, len(events))
	for _, ev := range events {
		out = append(out, adminAuditEventDTO{
			At:     ev.At.UTC(),
			Actor:  ev.Actor,
			Action: ev.Action,
			Target: ev.Target,
		})
	}
	writeAdminData(w, r, http.StatusOK, map[string]any{"events": out})
}

// --- Domain C: Support Tickets ---

func toAdminTicketDTO(t kb.Ticket, includeMessages bool) adminTicketDTO {
	dto := adminTicketDTO{
		ID:            t.ID,
		PublicRef:     t.PublicRef,
		AccountID:     t.AccountID,
		Username:      t.Username,
		Category:      t.Category,
		Title:         t.Title,
		CharacterGUID: t.CharacterGUID,
		CharacterName: t.CharacterName,
		Status:        t.Status,
		CreatedAt:     t.CreatedAt.UTC(),
		UpdatedAt:     t.UpdatedAt.UTC(),
		ClosedBy:      t.ClosedBy,
	}
	if includeMessages {
		dto.Messages = make([]adminTicketMsgDTO, 0, len(t.Messages))
		for _, m := range t.Messages {
			dto.Messages = append(dto.Messages, adminTicketMsgDTO{
				ID:        m.ID,
				TicketID:  m.TicketID,
				Author:    m.Author,
				FromStaff: m.FromStaff,
				Body:      m.Body,
				CreatedAt: m.CreatedAt.UTC(),
			})
		}
	}
	return dto
}

func (s *Server) adminTicketsRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if s.kb == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Ticket storage is not available.")
		return
	}
	switch len(rest) {
	case 0:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			adminMethodNotAllowed(w, r, "GET, HEAD")
			return
		}
		statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
		var accID uint32
		if v := strings.TrimSpace(r.URL.Query().Get("account_id")); v != "" {
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil || n == 0 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "account_id must be a positive integer.")
				return
			}
			accID = uint32(n)
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "limit must be an integer between 1 and 100.")
				return
			}
			limit = n
		}
		offset := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_parameter", "offset must be a non-negative integer.")
				return
			}
			offset = n
		}
		list, total, err := s.kb.ListTickets(r.Context(), statusFilter, accID, limit, offset)
		if err != nil {
			if errors.Is(err, kb.ErrBadStatus) {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_status", "status must be one of: active, open, in-progress, done, closed, all.")
				return
			}
			writeManagementError(w, r, http.StatusServiceUnavailable, "tickets_unavailable", "Could not load tickets.")
			return
		}
		out := make([]adminTicketDTO, 0, len(list))
		for _, t := range list {
			out = append(out, toAdminTicketDTO(t, false))
		}
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"tickets": out,
			"total":   total,
			"limit":   limit,
			"offset":  offset,
		})
	case 1:
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil || id < 1 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_id", "Invalid ticket ID.")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			t, err := s.kb.GetTicket(r.Context(), id)
			if err != nil {
				if errors.Is(err, kb.ErrTicketNotFound) {
					writeManagementError(w, r, http.StatusNotFound, "ticket_not_found", "Ticket not found.")
					return
				}
				writeManagementError(w, r, http.StatusServiceUnavailable, "tickets_unavailable", "Could not load ticket.")
				return
			}
			writeAdminData(w, r, http.StatusOK, toAdminTicketDTO(t, true))
		case http.MethodPatch:
			t, err := s.kb.GetTicket(r.Context(), id)
			if err != nil {
				if errors.Is(err, kb.ErrTicketNotFound) {
					writeManagementError(w, r, http.StatusNotFound, "ticket_not_found", "Ticket not found.")
					return
				}
				writeManagementError(w, r, http.StatusServiceUnavailable, "tickets_unavailable", "Could not load ticket.")
				return
			}
			var req struct {
				Status string `json:"status"`
				Body   string `json:"body"`
			}
			if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
				return
			}
			st := strings.TrimSpace(req.Status)
			body := strings.TrimSpace(req.Body)
			if st == "" && body == "" {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_request", "Provide status and/or body to update the ticket.")
				return
			}
			if st != "" && !kb.ValidTicketStatus(st) {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_status", "status must be one of: open, in-progress, done, closed.")
				return
			}
			if body != "" && len(body) > 4000 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_body", "body must be 1-4000 characters.")
				return
			}
			var notes []string
			if st != "" && st != t.Status {
				if err := s.kb.SetTicketStatus(r.Context(), t.ID, st, actor); err != nil {
					writeManagementError(w, r, http.StatusServiceUnavailable, "ticket_update_failed", "Could not update ticket status.")
					return
				}
				s.logStaff(actor, "ticket-status", t.PublicRef+"="+st)
				notes = append(notes, "Staff changed the status.")
			}
			if body != "" {
				if err := s.kb.AddTicketMessage(r.Context(), t.ID, actor, true, body); err != nil {
					writeManagementError(w, r, http.StatusServiceUnavailable, "ticket_update_failed", "Could not add ticket comment.")
					return
				}
				s.logStaff(actor, "ticket-reply", t.PublicRef)
				notes = append(notes, "Staff replied.")
			}
			fresh, err := s.kb.GetTicket(r.Context(), t.ID)
			if err != nil {
				fresh = t
			}
			if len(notes) > 0 {
				s.notifyTicketPlayer(fresh, strings.Join(notes, " "))
			}
			writeAdminData(w, r, http.StatusOK, toAdminTicketDTO(fresh, true))
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, PATCH")
		}
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

// --- Domain D: Content & Downloads (Knowledge Base & Downloads) ---

func toAdminArticleDTO(a kb.Article, includeBody bool) adminArticleDTO {
	dto := adminArticleDTO{
		ID:        a.ID,
		Slug:      a.Slug,
		Title:     a.Title,
		Summary:   a.Summary,
		Category:  a.Category,
		SortOrder: a.SortOrder,
		Published: a.Published,
		CreatedBy: a.CreatedBy,
		UpdatedBy: a.UpdatedBy,
		CreatedAt: a.CreatedAt.UTC(),
		UpdatedAt: a.UpdatedAt.UTC(),
	}
	if includeBody {
		dto.BodyMarkdown = a.BodyMarkdown
		dto.HTML = md.HTML(a.BodyMarkdown)
	}
	return dto
}

func (s *Server) adminKBRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if s.kb == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "Knowledge Base storage is not available.")
		return
	}
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			articles, err := s.kb.ListAll(r.Context())
			if err != nil {
				writeManagementError(w, r, http.StatusServiceUnavailable, "kb_unavailable", "Could not load Knowledge Base articles.")
				return
			}
			cats, _ := s.kb.Categories(r.Context())
			if cats == nil {
				cats = []string{}
			}
			out := make([]adminArticleDTO, 0, len(articles))
			for _, a := range articles {
				out = append(out, toAdminArticleDTO(a, false))
			}
			writeAdminData(w, r, http.StatusOK, map[string]any{
				"articles":   out,
				"categories": cats,
			})
		case http.MethodPost:
			var req struct {
				Title        string `json:"title"`
				Slug         string `json:"slug"`
				Category     string `json:"category"`
				Summary      string `json:"summary"`
				BodyMarkdown string `json:"body_markdown"`
				SortOrder    int    `json:"sort_order"`
				Published    bool   `json:"published"`
			}
			if !readAdminJSON(w, r, kbBodyMax, &req) {
				return
			}
			a := kb.Article{
				Title:        strings.TrimSpace(req.Title),
				Slug:         strings.TrimSpace(req.Slug),
				Category:     strings.TrimSpace(req.Category),
				Summary:      strings.TrimSpace(req.Summary),
				BodyMarkdown: req.BodyMarkdown,
				SortOrder:    req.SortOrder,
				Published:    req.Published,
				CreatedBy:    actor,
				UpdatedBy:    actor,
			}
			saved, err := s.kb.Create(r.Context(), a)
			if err != nil {
				s.writeAdminKBError(w, r, err)
				return
			}
			s.log.Info("admin api kb create", "actor", actor, "slug", saved.Slug, "id", saved.ID)
			s.logStaff(actor, "kb-create", saved.Slug)
			writeAdminData(w, r, http.StatusCreated, toAdminArticleDTO(saved, true))
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, POST")
		}
	case 1:
		idOrSlug := rest[0]
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			var (
				a   kb.Article
				err error
			)
			if id, perr := strconv.ParseInt(idOrSlug, 10, 64); perr == nil && id > 0 {
				a, err = s.kb.GetByID(r.Context(), id)
			} else if kb.ValidSlug(idOrSlug) {
				a, err = s.kb.GetBySlug(r.Context(), idOrSlug)
			} else {
				writeManagementError(w, r, http.StatusNotFound, "article_not_found", "Article not found.")
				return
			}
			if err != nil {
				if errors.Is(err, kb.ErrNotFound) {
					writeManagementError(w, r, http.StatusNotFound, "article_not_found", "Article not found.")
					return
				}
				writeManagementError(w, r, http.StatusServiceUnavailable, "kb_unavailable", "Could not load article.")
				return
			}
			writeAdminData(w, r, http.StatusOK, toAdminArticleDTO(a, true))
		case http.MethodPut:
			id, err := strconv.ParseInt(idOrSlug, 10, 64)
			if err != nil || id < 1 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_id", "Article ID must be a positive integer.")
				return
			}
			existing, err := s.kb.GetByID(r.Context(), id)
			if err != nil {
				if errors.Is(err, kb.ErrNotFound) {
					writeManagementError(w, r, http.StatusNotFound, "article_not_found", "Article not found.")
					return
				}
				writeManagementError(w, r, http.StatusServiceUnavailable, "kb_unavailable", "Could not load article.")
				return
			}
			var req struct {
				Title        string `json:"title"`
				Slug         string `json:"slug"`
				Category     string `json:"category"`
				Summary      string `json:"summary"`
				BodyMarkdown string `json:"body_markdown"`
				SortOrder    int    `json:"sort_order"`
				Published    bool   `json:"published"`
			}
			if !readAdminJSON(w, r, kbBodyMax, &req) {
				return
			}
			a := kb.Article{
				ID:           existing.ID,
				Title:        strings.TrimSpace(req.Title),
				Slug:         strings.TrimSpace(req.Slug),
				Category:     strings.TrimSpace(req.Category),
				Summary:      strings.TrimSpace(req.Summary),
				BodyMarkdown: req.BodyMarkdown,
				SortOrder:    req.SortOrder,
				Published:    req.Published,
				CreatedBy:    existing.CreatedBy,
				UpdatedBy:    actor,
			}
			saved, err := s.kb.Update(r.Context(), a)
			if err != nil {
				s.writeAdminKBError(w, r, err)
				return
			}
			s.log.Info("admin api kb update", "actor", actor, "slug", saved.Slug, "id", saved.ID)
			s.logStaff(actor, "kb-update", saved.Slug)
			writeAdminData(w, r, http.StatusOK, toAdminArticleDTO(saved, true))
		case http.MethodDelete:
			id, err := strconv.ParseInt(idOrSlug, 10, 64)
			if err != nil || id < 1 {
				writeManagementError(w, r, http.StatusBadRequest, "invalid_id", "Article ID must be a positive integer.")
				return
			}
			if err := s.kb.Delete(r.Context(), id); err != nil {
				if errors.Is(err, kb.ErrNotFound) {
					writeManagementError(w, r, http.StatusNotFound, "article_not_found", "Article not found.")
					return
				}
				writeManagementError(w, r, http.StatusServiceUnavailable, "kb_delete_failed", "Could not delete the article.")
				return
			}
			s.log.Info("admin api kb delete", "actor", actor, "id", id)
			s.logStaff(actor, "kb-delete", strconv.FormatInt(id, 10))
			writeAdminData(w, r, http.StatusOK, map[string]any{
				"deleted": true,
				"id":      id,
			})
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, PUT, DELETE")
		}
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

func (s *Server) writeAdminKBError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, kb.ErrSlugTaken):
		writeManagementError(w, r, http.StatusConflict, "slug_taken", "That slug is already used.")
	case errors.Is(err, kb.ErrInvalid):
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		writeManagementError(w, r, http.StatusBadRequest, "invalid_article", msg)
	default:
		s.log.Error("admin api kb save", "err", err)
		writeManagementError(w, r, http.StatusServiceUnavailable, "kb_save_failed", "Could not save the article.")
	}
}

func toAdminDownloadDTO(it downloads.Item) adminDownloadDTO {
	return adminDownloadDTO{
		ID:          it.ID,
		Title:       it.Title,
		Category:    it.Category,
		Label:       it.Label,
		Description: it.Description,
		Version:     it.Version,
		Featured:    it.Featured,
		FileName:    it.FileName,
		Size:        it.Size,
		SizeHuman:   it.SizeHuman,
		SHA256:      it.SHA256,
		Ready:       it.Ready,
		SourceURL:   it.SourceURL,
		AddonID:     it.AddonID,
		UploadedAt:  it.UploadedAt,
		ClamAV:      it.ClamAV,
		Mandatory:   it.Mandatory,
	}
}

func (s *Server) adminDownloadsRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if s.downloads == nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "downloads_unavailable", "Downloads store is not available.")
		return
	}
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			cat := strings.TrimSpace(r.URL.Query().Get("category"))
			items := s.downloads.List(cat)
			out := make([]adminDownloadDTO, 0, len(items))
			for _, it := range items {
				out = append(out, toAdminDownloadDTO(it))
			}
			writeAdminData(w, r, http.StatusOK, map[string]any{
				"writable":       s.downloads.Writable(),
				"scanning":       s.downloads.Scanning(),
				"max_bytes":      s.cfg.DownloadsMaxBytes(),
				"scan_max_bytes": downloadScanMax(s),
				"items":          out,
			})
		case http.MethodPost:
			s.adminDownloadsUpload(w, r, actor)
		default:
			adminMethodNotAllowed(w, r, "GET, HEAD, POST")
		}
	case 1:
		if r.Method != http.MethodDelete {
			adminMethodNotAllowed(w, r, "DELETE")
			return
		}
		id := strings.TrimSpace(rest[0])
		if err := s.downloads.Remove(id); err != nil {
			switch {
			case errors.Is(err, downloads.ErrInvalidID):
				writeManagementError(w, r, http.StatusBadRequest, "invalid_id", "Invalid download ID.")
			case errors.Is(err, downloads.ErrNotFound):
				writeManagementError(w, r, http.StatusNotFound, "download_not_found", "Download not found.")
			case errors.Is(err, downloads.ErrNotWritable):
				writeManagementError(w, r, http.StatusServiceUnavailable, "downloads_not_writable", "Downloads folder is not writable.")
			default:
				writeManagementError(w, r, http.StatusServiceUnavailable, "download_delete_failed", "Could not remove download.")
			}
			return
		}
		s.log.Info("admin api download delete", "actor", actor, "id", id)
		s.logStaff(actor, "download-delete", id)
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"deleted": true,
			"id":      strings.ToLower(id),
		})
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}

func (s *Server) adminDownloadsUpload(w http.ResponseWriter, r *http.Request, actor string) {
	max := s.cfg.DownloadsMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, max)
	mr, err := r.MultipartReader()
	if err != nil {
		writeManagementError(w, r, http.StatusBadRequest, "invalid_multipart", "Multipart form data with a file field is required.")
		return
	}
	fields := map[string]string{}
	var staged string
	var origName string
	defer func() {
		if staged != "" {
			_ = os.Remove(staged)
		}
	}()

	if err := os.MkdirAll(s.cfg.DownloadsDir, 0o755); err != nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "downloads_not_writable", uploadErr(downloads.ErrNotWritable, max))
		return
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeManagementError(w, r, http.StatusBadRequest, "upload_failed", uploadErr(err, max))
			return
		}
		if part.FormName() == "file" {
			origName = part.FileName()
			f, err := os.CreateTemp(s.cfg.DownloadsDir, ".up-*.part")
			if err != nil {
				writeManagementError(w, r, http.StatusServiceUnavailable, "downloads_not_writable", uploadErr(downloads.ErrNotWritable, max))
				return
			}
			_, copyErr := io.Copy(f, part)
			name := f.Name()
			_ = f.Close()
			if copyErr != nil {
				_ = os.Remove(name)
				writeManagementError(w, r, http.StatusBadRequest, "upload_failed", uploadErr(copyErr, max))
				return
			}
			staged = name
			continue
		}
		val, err := io.ReadAll(io.LimitReader(part, 8<<10))
		if err != nil {
			writeManagementError(w, r, http.StatusBadRequest, "upload_failed", "Could not read multipart field.")
			return
		}
		fields[part.FormName()] = string(val)
	}

	if staged == "" {
		writeManagementError(w, r, http.StatusBadRequest, "missing_file", "Choose a file to upload.")
		return
	}
	f, err := os.Open(staged)
	if err != nil {
		writeManagementError(w, r, http.StatusServiceUnavailable, "upload_failed", "Could not read staged upload.")
		return
	}
	cat := fields["category"]
	in := downloads.UploadInput{
		ID:          strings.TrimSpace(fields["id"]),
		Category:    cat,
		FileName:    origName,
		Title:       strings.TrimSpace(fields["title"]),
		Version:     strings.TrimSpace(fields["version"]),
		Description: strings.TrimSpace(fields["description"]),
		SourceURL:   strings.TrimSpace(fields["source_url"]),
		AddonID:     strings.TrimSpace(fields["addon_id"]),
		Mandatory:   fields["mandatory"] == "1" || fields["mandatory"] == "true" || fields["mandatory"] == "on",
	}
	if downloads.NormalizeCategory(cat) == downloads.CatMods && strings.TrimSpace(fields["addon_version"]) != "" {
		in.Version = strings.TrimSpace(fields["addon_version"])
	}
	added, addErr := s.downloads.Add(r.Context(), in, f)
	_ = f.Close()
	if addErr != nil {
		writeManagementError(w, r, http.StatusBadRequest, "upload_rejected", uploadErr(addErr, max))
		return
	}
	s.log.Info("admin api download upload", "actor", actor, "id", added.ID, "file", added.FileName, "bytes", added.Size)
	s.logStaff(actor, "download-upload", added.ID)
	writeAdminData(w, r, http.StatusCreated, toAdminDownloadDTO(added))
}

// --- Domain E: In-Game / Worldserver Actions ---

func (s *Server) adminGameRoute(w http.ResponseWriter, r *http.Request, actor string, rest []string) {
	if len(rest) != 1 {
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
		return
	}
	if r.Method != http.MethodPost {
		adminMethodNotAllowed(w, r, "POST")
		return
	}
	switch rest[0] {
	case "unstuck":
		if s.status == nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "status_unavailable", "Status service is not available.")
			return
		}
		var req struct {
			GUID uint32 `json:"guid"`
			Name string `json:"name"`
		}
		if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if req.GUID == 0 && req.Name == "" {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_request", "Provide character guid or name.")
			return
		}
		res, err := s.status.UnstuckByAdmin(r.Context(), req.GUID, req.Name)
		if err != nil {
			switch {
			case errors.Is(err, status.ErrCharNotFound):
				writeManagementError(w, r, http.StatusNotFound, "character_not_found", "Character not found.")
			case errors.Is(err, status.ErrCharOnline):
				writeManagementError(w, r, http.StatusConflict, "character_online", "Character is currently online; log out of the game first.")
			case errors.Is(err, status.ErrNoHomebind):
				writeManagementError(w, r, http.StatusUnprocessableEntity, "no_homebind", "Character does not have a hearth bind yet.")
			default:
				s.log.Error("admin api unstuck", "err", err, "actor", actor, "guid", req.GUID, "name", req.Name)
				writeManagementError(w, r, http.StatusServiceUnavailable, "unstuck_failed", "Could not unstuck the character.")
			}
			return
		}
		s.log.Info("admin api unstuck", "actor", actor, "char", res.Name, "guid", res.GUID, "via", res.Via)
		s.logStaff(actor, "unstuck", res.Name)
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"guid":      res.GUID,
			"name":      res.Name,
			"from_map":  res.FromMap,
			"from_zone": res.FromZone,
			"to_map":    res.ToMap,
			"to_zone":   res.ToZone,
			"via":       res.Via,
		})
	case "announce":
		var req struct {
			Message string `json:"message"`
			Mode    string `json:"mode"`
		}
		if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
			return
		}
		msg := strings.TrimSpace(req.Message)
		if msg == "" || len(msg) > 255 {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_message", "message must be 1-255 characters.")
			return
		}
		mode := strings.ToLower(strings.TrimSpace(req.Mode))
		if mode == "" {
			mode = "announce"
		}
		if mode != "announce" && mode != "notify" && mode != "both" {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_mode", "mode must be announce, notify, or both.")
			return
		}
		soapClient := s.status.SOAP()
		if soapClient == nil {
			if s.cfg.DemoMode {
				s.logStaff(actor, "game-announce", mode+": "+msg)
				writeAdminData(w, r, http.StatusOK, map[string]any{
					"sent":    true,
					"mode":    mode,
					"message": msg,
					"demo":    true,
				})
				return
			}
			writeManagementError(w, r, http.StatusServiceUnavailable, "soap_unavailable", "Worldserver SOAP is not configured.")
			return
		}
		if err := soapClient.Announce(r.Context(), msg, mode); err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "soap_error", "Worldserver announcement failed.")
			return
		}
		s.log.Info("admin api announce", "actor", actor, "mode", mode)
		s.logStaff(actor, "game-announce", mode+": "+msg)
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"sent":    true,
			"mode":    mode,
			"message": msg,
		})
	case "soap":
		var req struct {
			Command string `json:"command"`
		}
		if !readAdminJSON(w, r, adminJSONBodyMax, &req) {
			return
		}
		cmd := strings.TrimSpace(req.Command)
		if err := soap.ValidateSafeCommand(cmd); err != nil {
			writeManagementError(w, r, http.StatusBadRequest, "invalid_command", err.Error())
			return
		}
		soapClient := s.status.SOAP()
		if soapClient == nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "soap_unavailable", "Worldserver SOAP is not configured.")
			return
		}
		out, err := soapClient.Execute(r.Context(), cmd)
		if err != nil {
			writeManagementError(w, r, http.StatusServiceUnavailable, "soap_error", "Worldserver SOAP command could not be executed.")
			return
		}
		s.log.Info("admin api soap", "actor", actor, "command", cmd)
		s.logStaff(actor, "game-soap", cmd)
		writeAdminData(w, r, http.StatusOK, map[string]any{
			"command": cmd,
			"result":  strings.TrimSpace(out),
		})
	default:
		writeManagementError(w, r, http.StatusNotFound, "not_found", "Endpoint not found.")
	}
}
