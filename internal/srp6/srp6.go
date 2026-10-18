// Package srp6 implements AzerothCore / TrinityCore WoW 3.3.5a SRP6
// registration data (salt + verifier).
//
// Algorithm (https://www.azerothcore.org/wiki/account):
//
//	h1 = SHA1(UPPER(username) + ":" + UPPER(password))
//	h2 = SHA1(salt || h1)
//	x  = h2 as little-endian integer
//	v  = g^x mod N   (little-endian, 32 bytes)
//
// g = 7
// N = 0x894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7
//
// This is the SQL fallback for account create / password change. Prefer SOAP
// so the core generates salt + verifier itself.
package srp6

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

const (
	SaltLength     = 32
	VerifierLength = 32
)

var (
	gInt = big.NewInt(7)
	nInt = mustN()
)

func mustN() *big.Int {
	n, ok := new(big.Int).SetString("894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7", 16)
	if !ok {
		panic("srp6: invalid N")
	}
	return n
}

// UpperLatin uppercases ASCII a-z only, matching AzerothCore Utf8ToUpperOnlyLatin
// for the Latin subset used by this site (alphanumeric usernames, typical passwords).
func UpperLatin(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(unicode.ToUpper(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func CalculateVerifier(username, password string, salt []byte) ([]byte, error) {
	if len(salt) != SaltLength {
		return nil, fmt.Errorf("srp6: salt must be %d bytes", SaltLength)
	}
	u := UpperLatin(username)
	p := UpperLatin(password)

	h1 := sha1.Sum([]byte(u + ":" + p))
	h2in := make([]byte, 0, SaltLength+sha1.Size)
	h2in = append(h2in, salt...)
	h2in = append(h2in, h1[:]...)
	h2 := sha1.Sum(h2in)

	x := new(big.Int).SetBytes(reverse(h2[:]))
	v := new(big.Int).Exp(gInt, x, nInt)
	return padLE(v, VerifierLength), nil
}

func MakeRegistrationData(username, password string) (salt, verifier []byte, err error) {
	salt = make([]byte, SaltLength)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("srp6: salt: %w", err)
	}
	verifier, err = CalculateVerifier(username, password, salt)
	if err != nil {
		return nil, nil, err
	}
	return salt, verifier, nil
}

func CheckLogin(username, password string, salt, verifier []byte) bool {
	got, err := CalculateVerifier(username, password, salt)
	if err != nil {
		return false
	}
	if len(got) != len(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare(got, verifier) == 1
}

func reverse(in []byte) []byte {
	out := make([]byte, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func padLE(v *big.Int, n int) []byte {
	be := v.Bytes()
	if len(be) > n {
		be = be[len(be)-n:]
	}
	le := reverse(be)
	if len(le) == n {
		return le
	}
	out := make([]byte, n)
	copy(out, le)
	return out
}
