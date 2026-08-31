package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	hashPrefix              = "argon2id"
	migratedSentinel        = "!migrated"
	argonTime        uint32 = 1
	argonMemory      uint32 = 64 * 1024
	argonThreads     uint8  = 4
	argonKeyLen      uint32 = 32
	saltLen                 = 16
)

func HashPassword(password string) (string, error) {
	_, hash, err := HashPasswordAndKey(password)
	return hash, err
}

func HashPasswordAndKey(password string) (key []byte, hash string, err error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, "", err
	}
	key = argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	hash = fmt.Sprintf("%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		hashPrefix, argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return key, hash, nil
}

func NeedsLegacy(hash string) bool {
	return hash == "" || hash == migratedSentinel
}

func CheckPassword(hash, password string) bool {
	_, ok := VerifyAndKey(hash, password)
	return ok
}

func VerifyAndKey(hash, password string) ([]byte, bool) {
	if NeedsLegacy(hash) || !strings.HasPrefix(hash, hashPrefix+"$") {
		return nil, false
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 5 {
		return nil, false
	}
	var mem, timeCost uint32
	var threads uint8
	for _, kv := range strings.Split(parts[2], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(v, 10, 32)
		switch k {
		case "m":
			mem = uint32(n)
		case "t":
			timeCost = uint32(n)
		case "p":
			threads = uint8(n)
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, mem, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(want, got) != 1 {
		return nil, false
	}
	return got, true
}
