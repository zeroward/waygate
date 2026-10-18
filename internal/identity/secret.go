package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const dekLen = 32

func newDEK() ([]byte, error) {
	k := make([]byte, dekLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

func seal(key, plaintext []byte) (nonce, blob []byte, err error) { return sealAAD(key, plaintext, nil) }

func sealAAD(key, plaintext, aad []byte) (nonce, blob []byte, err error) {
	if len(key) != dekLen {
		return nil, nil, errors.New("bad key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	blob = gcm.Seal(nil, nonce, plaintext, aad)
	return nonce, blob, nil
}

func open(key, nonce, blob []byte) ([]byte, error) { return openAAD(key, nonce, blob, nil) }

func openAAD(key, nonce, blob, aad []byte) ([]byte, error) {
	if len(key) != dekLen || len(blob) == 0 {
		return nil, errors.New("bad secret")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("bad nonce")
	}
	return gcm.Open(nil, nonce, blob, aad)
}

func wrapAAD(userID uint32) []byte { return []byte(fmt.Sprintf("waygate:dek-wrap:v2:user:%d", userID)) }
