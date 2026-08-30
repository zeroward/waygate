package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/zeroward/waygate/internal/clamav"
	"github.com/zeroward/waygate/internal/downloads"
)

func (s *Server) staffDownloadPOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	dest := "/staff#downloads"
	max := s.cfg.DownloadsMaxBytes()
	r.Body = http.MaxBytesReader(w, r.Body, max)
	mr, err := r.MultipartReader()
	if err != nil {
		s.uploadReply(w, r, dest, "error", "Choose a file to upload.")
		return
	}

	fields := map[string]string{}
	var staged string
	var origName string
	var added downloads.Item
	var addedOK bool
	defer func() {
		if staged != "" {
			_ = os.Remove(staged)
		}
	}()

	addFrom := func(src io.Reader, name string) error {
		if !sess.ValidCSRF(fields["csrf_token"]) {
			return errBadCSRF
		}
		cat := fields["category"]
		in := downloads.UploadInput{
			Category:    cat,
			FileName:    name,
			Title:       strings.TrimSpace(fields["title"]),
			Version:     strings.TrimSpace(fields["version"]),
			Description: strings.TrimSpace(fields["description"]),
			SourceURL:   strings.TrimSpace(fields["source_url"]),
			AddonID:     strings.TrimSpace(fields["addon_id"]),
			Mandatory:   fields["mandatory"] == "1" || fields["mandatory"] == "on",
		}
		if downloads.NormalizeCategory(cat) == downloads.CatMods && strings.TrimSpace(fields["addon_version"]) != "" {
			in.Version = strings.TrimSpace(fields["addon_version"])
		}
		it, err := s.downloads.Add(r.Context(), in, src)
		if err != nil {
			return err
		}
		added = it
		addedOK = true
		return nil
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.uploadReply(w, r, dest, "error", uploadErr(err, max))
			return
		}
		if part.FormName() == "file" {
			origName = part.FileName()
			f, err := os.CreateTemp(s.cfg.DownloadsDir, ".up-*.part")
			if err != nil {
				s.uploadReply(w, r, dest, "error", uploadErr(downloads.ErrNotWritable, max))
				return
			}
			_, copyErr := io.Copy(f, part)
			name := f.Name()
			_ = f.Close()
			if copyErr != nil {
				_ = os.Remove(name)
				s.uploadReply(w, r, dest, "error", uploadErr(copyErr, max))
				return
			}
			staged = name
			continue
		}
		val, err := io.ReadAll(io.LimitReader(part, 8<<10))
		if err != nil {
			s.uploadReply(w, r, dest, "error", "Could not read the upload.")
			return
		}
		fields[part.FormName()] = string(val)
	}

	if !addedOK {
		if staged == "" {
			s.uploadReply(w, r, dest, "error", "Choose a file to upload.")
			return
		}
		f, err := os.Open(staged)
		if err != nil {
			s.uploadReply(w, r, dest, "error", "Could not read the upload.")
			return
		}
		err = addFrom(f, origName)
		_ = f.Close()
		if err != nil {
			s.uploadReply(w, r, dest, "error", uploadErr(err, max))
			return
		}
	}

	s.log.Info("staff download upload", "actor", sess.User.Username, "id", added.ID, "file", added.FileName, "bytes", added.Size)
	s.uploadReply(w, r, dest, "success", "Uploaded "+added.Title+".")
}

func downloadScanMax(s *Server) int64 {
	// Scanning is off; keep the progress UI on "Saving…" after the bytes land.
	_ = s
	return 0
}

func wantsJSON(r *http.Request) bool {
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (s *Server) uploadReply(w http.ResponseWriter, r *http.Request, dest, kind, text string) {
	if !wantsJSON(r) {
		s.flashRedirect(w, r, dest, kind, text)
		return
	}
	s.sessions.GetOrCreate(w, r).SetFlash(kind, text)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	code := http.StatusOK
	if kind == "error" {
		code = http.StatusBadRequest
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":       kind != "error",
		"message":  text,
		"redirect": dest,
	})
}

func (s *Server) staffDownloadDeletePOST(w http.ResponseWriter, r *http.Request) {
	sess := s.requireStaff(w, r)
	if sess == nil {
		return
	}
	dest := "/staff#downloads"
	if !s.parseForm(w, r) || !s.requireCSRF(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if err := s.downloads.Remove(id); err != nil {
		s.flashRedirect(w, r, dest, "error", uploadErr(err, 0))
		return
	}
	s.log.Info("staff download delete", "actor", sess.User.Username, "id", id)
	s.flashRedirect(w, r, dest, "success", "Removed "+id+".")
}

var errBadCSRF = errors.New("invalid request token")

func uploadErr(err error, max int64) string {
	switch {
	case errors.Is(err, errBadCSRF):
		return "Invalid request token. Reload the page and try again."
	case errors.Is(err, downloads.ErrNotWritable):
		return "Downloads folder is not writable. Mount it read-write and let the process user write it."
	case errors.Is(err, downloads.ErrBadFile):
		return "Use one of: " + downloads.AllowedExtHint() + "."
	case errors.Is(err, downloads.ErrEmptyFile):
		return "File is empty."
	case errors.Is(err, downloads.ErrInvalidName):
		return "File name is not allowed."
	case errors.Is(err, downloads.ErrInvalidID):
		return "Invalid download id."
	case errors.Is(err, downloads.ErrNotFound):
		return "Download not found."
	case errors.Is(err, downloads.ErrNeedName):
		return err.Error()
	case errors.Is(err, downloads.ErrNeedVersion):
		return err.Error()
	case errors.Is(err, downloads.ErrBadSource):
		return err.Error()
	case errors.Is(err, downloads.ErrBadAddonID):
		return err.Error()
	case errors.Is(err, clamav.ErrInfected):
		var inf *clamav.InfectedError
		if errors.As(err, &inf) && inf.Signature != "" {
			return "This file failed the virus scan (" + inf.Signature + ")."
		}
		return "This file failed the virus scan."
	case errors.Is(err, clamav.ErrUnavailable):
		return "Virus scanner is unavailable. Try again in a minute."
	default:
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			if max > 0 {
				return "File is too large (max " + downloads.HumanSize(max) + ")."
			}
			return "File is too large."
		}
		return "Could not save the file."
	}
}
