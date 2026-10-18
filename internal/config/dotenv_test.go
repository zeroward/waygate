package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("# synthetic fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("environment file not private", err)
	}
}
