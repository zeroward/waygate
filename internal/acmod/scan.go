// Package acmod lists AzerothCore modules from the server modules/ directory
// (the same tree worldserver compiles) plus optional world.module_string rows.
package acmod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type Module struct {
	ID      string
	Title   string
	Blurb   string
	Version string
}

func ScanDir(root string) []Module {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Module
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		if !looksLikeModule(name, filepath.Join(root, name)) {
			continue
		}
		m := Module{
			ID:    name,
			Title: prettyTitle(name),
		}
		dir := filepath.Join(root, name)
		if meta, err := os.ReadFile(filepath.Join(dir, "acore-module.json")); err == nil {
			var j struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if json.Unmarshal(meta, &j) == nil {
				if j.Version != "" {
					m.Version = j.Version
				}
				if j.Name != "" && m.Title == prettyTitle(name) {
					// keep pretty title; json name is usually the folder slug
				}
			}
		}
		m.Blurb = readmeBlurb(dir)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

func Merge(base []Module, extra []Module) []Module {
	seen := map[string]bool{}
	for _, m := range base {
		seen[strings.ToLower(m.ID)] = true
	}
	out := append([]Module(nil), base...)
	for _, m := range extra {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == "" || seen[id] {
			continue
		}
		if m.Title == "" {
			m.Title = prettyTitle(m.ID)
		}
		seen[id] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

func looksLikeModule(name, dir string) bool {
	if strings.HasPrefix(name, "mod-") || strings.HasPrefix(name, "mod_") {
		return true
	}
	for _, child := range []string{"src", "conf", "acore-module.json"} {
		if _, err := os.Stat(filepath.Join(dir, child)); err == nil {
			return true
		}
	}
	return false
}

func prettyTitle(id string) string {
	s := strings.TrimSpace(id)
	s = strings.TrimPrefix(s, "mod-")
	s = strings.TrimPrefix(s, "mod_")
	s = strings.ReplaceAll(s, "_", "-")
	parts := strings.Split(s, "-")
	for i, p := range parts {
		parts[i] = prettyWord(p)
	}
	return strings.Join(parts, " ")
}

func prettyWord(w string) string {
	w = strings.ToLower(strings.TrimSpace(w))
	if w == "" {
		return w
	}
	switch w {
	case "ah":
		return "AH"
	case "aoe":
		return "AoE"
	case "llm":
		return "LLM"
	case "npc":
		return "NPC"
	case "gm":
		return "GM"
	case "pvp":
		return "PvP"
	case "hd":
		return "HD"
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func readmeBlurb(dir string) string {
	for _, name := range []string{"README.md", "Readme.md", "readme.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if s := firstBlurb(string(b)); s != "" {
			return s
		}
	}
	return ""
}

func firstBlurb(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	var buf strings.Builder
	var paras []string
	flush := func() {
		t := strings.TrimSpace(buf.String())
		buf.Reset()
		if t != "" {
			paras = append(paras, t)
		}
	}
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			flush()
			continue
		}
		if strings.HasPrefix(t, "![") || strings.HasPrefix(t, "[![") || strings.HasPrefix(t, "<") {
			continue
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.Trim(t, "*_ ")
		if t == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(t)
	}
	flush()
	for _, p := range paras {
		low := strings.ToLower(p)
		if strings.Contains(low, "build status") || strings.Contains(low, "badge.svg") {
			continue
		}
		if strings.Contains(low, "azerothcore") && len(p) < 48 {
			continue
		}
		if strings.HasPrefix(low, "beta testing") {
			continue
		}
		return clip(p, 160)
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	s = s[:n]
	if i := strings.LastIndex(s, " "); i > 80 {
		s = s[:i]
	}
	return strings.TrimRight(s, " ,;:") + "…"
}
