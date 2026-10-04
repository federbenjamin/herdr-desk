package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ReadToken returns the desk's bearer token. A missing file is an error that wraps fs.ErrNotExist.
func ReadToken(p Paths) (string, error) {
	b, err := os.ReadFile(p.TokenFile())
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("%s is empty", p.TokenFile())
	}
	return t, nil
}

// WriteToken writes the token file, 0600 in a 0700 folder.
func WriteToken(p Paths, token string) error {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("a token is one word with no spaces")
	}
	return writeFileAtomic(p.TokenFile(), []byte(token+"\n"))
}

// RotateToken mints a new token (32 random bytes as hex), writes it, and returns it.
func RotateToken(p Paths) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := hex.EncodeToString(b)
	if err := WriteToken(p, t); err != nil {
		return "", err
	}
	return t, nil
}
