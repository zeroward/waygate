package identity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Vault is a keyring held outside SQLite. Keep retired keys until all DEKs have
// been rewrapped; UnlockSSO lazily rotates wrappers to the active key.
type Vault struct {
	Active string
	keys   map[string][]byte
}

func LoadVault(path string) (*Vault, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read SSO vault key file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("SSO vault key file must be a regular private file (0600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var raw struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid SSO vault key file")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid SSO vault key file")
	}
	v := &Vault{Active: raw.Active, keys: map[string][]byte{}}
	for id, value := range raw.Keys {
		b, e := base64.StdEncoding.DecodeString(value)
		if e != nil || len(b) != 32 || id == "" || len(id) > 80 {
			return nil, fmt.Errorf("invalid SSO vault key")
		}
		v.keys[id] = b
	}
	if len(v.keys[v.Active]) != 32 {
		return nil, fmt.Errorf("SSO vault active key is missing")
	}
	return v, nil
}
func vaultAAD(id uint32, key string) []byte {
	return []byte(fmt.Sprintf("waygate:sso-dek:v3:user:%d:key:%s", id, key))
}
func (v *Vault) seal(id uint32, dek []byte) ([]byte, []byte, error) {
	return sealAAD(v.keys[v.Active], dek, vaultAAD(id, v.Active))
}
func (v *Vault) open(id uint32, key string, nonce, wrap []byte) ([]byte, error) {
	return openAAD(v.keys[key], nonce, wrap, vaultAAD(id, key))
}
