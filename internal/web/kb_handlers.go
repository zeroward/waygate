package web

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zeroward/waygate/internal/account"
	"github.com/zeroward/waygate/internal/kb"
	"github.com/zeroward/waygate/internal/md"
	"github.com/zeroward/waygate/internal/session"
)

const kbEditorMin uint8 = account.RankAdmin // GM 3+
const kbBodyMax = 256 << 10

func (s *Server) canEditKB(u *session.User) bool {
	return u != nil && u.GMLevel >= kbEditorMin
}

func (s *Server) requireKBEditor(w http.ResponseWriter, r *http.Request) *session.Session {
	sess := s.sessions.GetOrCreate(w, r)
	if sess.User == nil {
		s.flashRedirect(w, r, "/account?next="+url.QueryEscape(r.URL.RequestURI()), "error", "Log in first.")
		return nil
	}
	if !s.canEditKB(sess.User) {
		http.Error(w, "Knowledge Base editing requires Admin (GM 3) or higher.", http.StatusForbidden)
		return nil
	}
	return sess
}

func (s *Server) connectRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/kb/how-to-connect", http.StatusMovedPermanently)
}

func (s *Server) realmlistFile(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	host := strings.TrimSpace(s.cfg.PublicHost)
	host = strings.ReplaceAll(strings.ReplaceAll(host, "\r", ""), "\n", "")
	if host == "" {
		host = "127.0.0.1"
	}
	body := "set realmlist " + host + "\r\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="realmlist.wtf"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

type kbGroup struct {
	Name     string
	Articles []kb.Article
}

func groupArticles(list []kb.Article) []kbGroup {
	var groups []kbGroup
	idx := map[string]int{}
	for _, a := range list {
		i, ok := idx[a.Category]
		if !ok {
			idx[a.Category] = len(groups)
			groups = append(groups, kbGroup{Name: a.Category})
			i = len(groups) - 1
		}
		groups[i].Articles = append(groups[i].Articles, a)
	}
	return groups
}

func (s *Server) kbIndex(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 64 {
		q = q[:64]
	}
	list, err := s.kb.ListPublished(r.Context(), q)
	if err != nil {
		s.log.Error("kb list", "err", err)
		http.Error(w, "Could not load the knowledge base.", http.StatusInternalServerError)
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	canEdit := s.canEditKB(sess.User)
	var staff []kb.Article
	if canEdit {
		staff, err = s.kb.ListAll(r.Context())
		if err != nil {
			s.log.Error("kb staff list", "err", err)
		}
	}
	s.view(w, r, "kb.html", "Knowledge Base", "kb", map[string]any{
		"Query":   q,
		"Groups":  groupArticles(list),
		"CanEdit": canEdit,
		"Staff":   staff,
	})
}

func (s *Server) kbArticle(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) == nil {
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !kb.ValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	a, err := s.kb.GetBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, kb.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("kb get", "err", err, "slug", slug)
		http.Error(w, "Could not load the article.", http.StatusInternalServerError)
		return
	}
	sess := s.sessions.GetOrCreate(w, r)
	canEdit := s.canEditKB(sess.User)
	preview := r.URL.Query().Get("preview") == "1"
	if !a.Published {
		if !canEdit {
			http.NotFound(w, r)
			return
		}
		if !preview {
			http.Redirect(w, r, "/kb/"+a.Slug+"?preview=1", http.StatusSeeOther)
			return
		}
	}
	peers, err := s.kb.CategoryPublished(r.Context(), a.Category)
	if err != nil {
		s.log.Error("kb peers", "err", err)
	}
	var prev, next *kb.Article
	for i := range peers {
		if peers[i].ID == a.ID {
			if i > 0 {
				p := peers[i-1]
				prev = &p
			}
			if i+1 < len(peers) {
				n := peers[i+1]
				next = &n
			}
			break
		}
	}
	s.view(w, r, "kb_article.html", a.Title, "kb", map[string]any{
		"Article": a,
		"HTML":    template.HTML(md.HTML(a.BodyMarkdown)),
		"Updated": a.UpdatedAt.UTC().Format("2 January 2006"),
		"Peers":   peers,
		"Prev":    prev,
		"Next":    next,
		"CanEdit": canEdit,
		"Preview": preview && !a.Published,
		"Draft":   !a.Published,
	})
}

func (s *Server) staffKB(w http.ResponseWriter, r *http.Request) {
	if s.requireKBEditor(w, r) == nil {
		return
	}
	list, err := s.kb.ListAll(r.Context())
	if err != nil {
		s.log.Error("staff kb list", "err", err)
		s.flashRedirect(w, r, "/kb", "error", "Could not load articles.")
		return
	}
	s.view(w, r, "kb_staff.html", "Knowledge Base", "kb", map[string]any{
		"Articles": list,
	})
}

func (s *Server) staffKBNew(w http.ResponseWriter, r *http.Request) {
	if s.requireKBEditor(w, r) == nil {
		return
	}
	cats, _ := s.kb.Categories(r.Context())
	s.view(w, r, "kb_edit.html", "New article", "kb", map[string]any{
		"Article":    kb.Article{Category: "Getting started", Published: false},
		"Categories": cats,
		"New":        true,
		"Preview":    template.HTML(""),
	})
}

func (s *Server) staffKBEdit(w http.ResponseWriter, r *http.Request) {
	if s.requireKBEditor(w, r) == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	a, err := s.kb.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cats, _ := s.kb.Categories(r.Context())
	s.view(w, r, "kb_edit.html", "Edit "+a.Title, "kb", map[string]any{
		"Article":    a,
		"Categories": cats,
		"New":        false,
		"Preview":    template.HTML(md.HTML(a.BodyMarkdown)),
	})
}

func (s *Server) staffKBCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.requireKBEditor(w, r)
	if sess == nil {
		return
	}
	if !s.parseFormMax(w, r, kbBodyMax) || !s.requireCSRF(w, r) {
		return
	}
	if !s.kbRL.Allow(s.ip(r) + ":" + sess.User.Username) {
		s.flashRedirect(w, r, "/staff/kb/new", "error", "Too many saves. Wait and try again.")
		return
	}
	a := articleFromForm(r)
	a.CreatedBy = sess.User.Username
	a.UpdatedBy = sess.User.Username
	saved, err := s.kb.Create(r.Context(), a)
	if err != nil {
		s.kbFormError(w, r, a, true, err)
		return
	}
	s.log.Info("kb create", "actor", sess.User.Username, "slug", saved.Slug, "id", saved.ID)
	s.flashRedirect(w, r, "/staff/kb/"+strconv.FormatInt(saved.ID, 10), "success", "Article saved.")
}

func (s *Server) staffKBUpdate(w http.ResponseWriter, r *http.Request) {
	sess := s.requireKBEditor(w, r)
	if sess == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if !s.parseFormMax(w, r, kbBodyMax) || !s.requireCSRF(w, r) {
		return
	}
	if !s.kbRL.Allow(s.ip(r) + ":" + sess.User.Username) {
		s.flashRedirect(w, r, "/staff/kb/"+strconv.FormatInt(id, 10), "error", "Too many saves. Wait and try again.")
		return
	}
	existing, err := s.kb.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a := articleFromForm(r)
	a.ID = existing.ID
	a.CreatedBy = existing.CreatedBy
	a.UpdatedBy = sess.User.Username
	saved, err := s.kb.Update(r.Context(), a)
	if err != nil {
		s.kbFormError(w, r, a, false, err)
		return
	}
	s.log.Info("kb update", "actor", sess.User.Username, "slug", saved.Slug, "id", saved.ID)
	s.flashRedirect(w, r, "/staff/kb/"+strconv.FormatInt(saved.ID, 10), "success", "Article saved.")
}

func (s *Server) staffKBDelete(w http.ResponseWriter, r *http.Request) {
	sess := s.requireKBEditor(w, r)
	if sess == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	if !s.kbRL.Allow(s.ip(r) + ":" + sess.User.Username) {
		s.flashRedirect(w, r, "/staff/kb/"+strconv.FormatInt(id, 10), "error", "Too many saves. Wait and try again.")
		return
	}
	if err := s.kb.Delete(r.Context(), id); err != nil {
		if errors.Is(err, kb.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.log.Error("kb delete", "err", err, "id", id)
		s.flashRedirect(w, r, "/staff/kb", "error", "Could not delete the article.")
		return
	}
	s.log.Info("kb delete", "actor", sess.User.Username, "id", id)
	s.flashRedirect(w, r, "/staff/kb", "success", "Article deleted.")
}

func (s *Server) staffKBPreview(w http.ResponseWriter, r *http.Request) {
	if s.requireKBEditor(w, r) == nil {
		return
	}
	if !s.parseFormMax(w, r, kbBodyMax) || !s.requireCSRF(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(md.HTML(r.FormValue("body_markdown"))))
}

func (s *Server) kbFormError(w http.ResponseWriter, r *http.Request, a kb.Article, isNew bool, err error) {
	msg := "Could not save the article."
	switch {
	case errors.Is(err, kb.ErrSlugTaken):
		msg = "That slug is already used."
	case errors.Is(err, kb.ErrInvalid):
		msg = err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
	default:
		s.log.Error("kb save", "err", err)
	}
	cats, _ := s.kb.Categories(r.Context())
	w.WriteHeader(http.StatusBadRequest)
	title := "Edit article"
	if isNew {
		title = "New article"
	}
	s.view(w, r, "kb_edit.html", title, "kb", map[string]any{
		"Article":    a,
		"Categories": cats,
		"New":        isNew,
		"Error":      msg,
		"Preview":    template.HTML(md.HTML(a.BodyMarkdown)),
	})
}

func articleFromForm(r *http.Request) kb.Article {
	sortN, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("sort_order")))
	return kb.Article{
		Title:        strings.TrimSpace(r.FormValue("title")),
		Slug:         strings.TrimSpace(r.FormValue("slug")),
		Category:     strings.TrimSpace(r.FormValue("category")),
		Summary:      strings.TrimSpace(r.FormValue("summary")),
		BodyMarkdown: r.FormValue("body_markdown"),
		SortOrder:    sortN,
		Published:    r.FormValue("published") == "1",
	}
}

func (s *Server) parseFormMax(w http.ResponseWriter, r *http.Request, n int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, n)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Request too large or malformed.", http.StatusBadRequest)
		return false
	}
	return true
}

func stripLeadingH1(mdSrc string) string {
	mdSrc = strings.TrimLeft(mdSrc, "\n")
	if !strings.HasPrefix(mdSrc, "# ") {
		return strings.TrimSpace(mdSrc)
	}
	if i := strings.IndexByte(mdSrc, '\n'); i >= 0 {
		return strings.TrimSpace(mdSrc[i+1:])
	}
	return ""
}
