package acmod

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrettyTitle(t *testing.T) {
	cases := map[string]string{
		"mod-playerbots":    "Playerbots",
		"mod-ah-bot":        "AH Bot",
		"mod-aoe-loot":      "AoE Loot",
		"mod-llm-chatter":   "LLM Chatter",
		"mod-npc-enchanter": "NPC Enchanter",
		"mod-fly-anywhere":  "Fly Anywhere",
		"mod-learn-spells":  "Learn Spells",
		"mod-transmog":      "Transmog",
		"mod-autobalance":   "Autobalance",
	}
	for in, want := range cases {
		if got := prettyTitle(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestScanDir(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "mod-aoe-loot")
	if err := os.MkdirAll(filepath.Join(mod, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "README.md"), []byte("# AoE Loot\n\nLoot every corpse in range with one click.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Mkdir(filepath.Join(root, "not-a-module"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "CMakeLists.txt"), []byte("x"), 0o644)

	got := ScanDir(root)
	if len(got) != 1 || got[0].ID != "mod-aoe-loot" || got[0].Title != "AoE Loot" {
		t.Fatalf("%+v", got)
	}
	if got[0].Blurb == "" {
		t.Fatal("expected blurb from README")
	}
}

func TestMerge(t *testing.T) {
	a := []Module{{ID: "mod-transmog", Title: "Transmog"}}
	b := []Module{{ID: "mod-transmog", Title: "dup"}, {ID: "mod-ah-bot", Title: ""}}
	got := Merge(a, b)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}
