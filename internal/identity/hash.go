package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	hashPrefix              = "waygate-argon2id-v2"
	migratedSentinel        = "!migrated"
	argonTime        uint32 = 1
	argonMemory      uint32 = 64 * 1024
	argonThreads     uint8  = 4
	argonKeyLen      uint32 = 32
	saltLen                 = 16
)

type passwordRecord struct {
	salt, verifier []byte
	legacy         bool
}

func parsePassword(hash string) (passwordRecord, error) {
	p := strings.Split(hash, "$")
	if len(p) != 5 || (p[0] != hashPrefix && p[0] != "argon2id") || p[1] != "v=19" || p[2] != "m=65536,t=1,p=4" {
		return passwordRecord{}, fmt.Errorf("unsupported password record")
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[3])
	if e != nil || len(salt) != saltLen {
		return passwordRecord{}, fmt.Errorf("invalid password salt")
	}
	v, e := base64.RawStdEncoding.DecodeString(p[4])
	if e != nil || len(v) != 32 {
		return passwordRecord{}, fmt.Errorf("invalid password verifier")
	}
	return passwordRecord{salt, v, p[0] == "argon2id"}, nil
}
func derive(master []byte, purpose string) []byte {
	out := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, master, nil, []byte("waygate:v2:"+purpose)), out); err != nil {
		panic(err)
	}
	return out
}
func encodePassword(salt, verifier []byte) string {
	return fmt.Sprintf("%s$v=19$m=65536,t=1,p=4$%s$%s", hashPrefix, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(verifier))
}
func HashPassword(password string) (string, error) {
	_, h, e := HashPasswordAndKey(password)
	return h, e
}
func HashPasswordAndKey(password string) ([]byte, string, error) {
	salt := make([]byte, saltLen)
	if _, e := rand.Read(salt); e != nil {
		return nil, "", e
	}
	master := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	defer clear(master)
	return derive(master, "dek-wrap"), encodePassword(salt, derive(master, "password-verifier")), nil
}
func NeedsLegacy(hash string) bool             { return hash == "" || hash == migratedSentinel }
func CheckPassword(hash, password string) bool { _, ok := VerifyAndKey(hash, password); return ok }
func VerifyAndKey(hash, password string) ([]byte, bool) {
	r, e := parsePassword(hash)
	if e != nil || r.legacy {
		return nil, false
	}
	master := argon2.IDKey([]byte(password), r.salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	defer clear(master)
	if subtle.ConstantTimeCompare(r.verifier, derive(master, "password-verifier")) != 1 {
		return nil, false
	}
	return derive(master, "dek-wrap"), true
}
