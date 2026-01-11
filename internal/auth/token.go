package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	TokenPrefix    = "ts_"
	TokenByteLen   = 32 // 256 bits
	BcryptCost     = 12
)

type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Hash      string     `json:"hash"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
}

func GenerateToken() (plaintext string, err error) {
	b := make([]byte, TokenByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return TokenPrefix + hex.EncodeToString(b), nil
}

func HashToken(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash token: %w", err)
	}
	return string(hash), nil
}

func VerifyToken(plaintext, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
	return err == nil
}

func GenerateTokenID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func IsValidTokenFormat(token string) bool {
	if !strings.HasPrefix(token, TokenPrefix) {
		return false
	}
	hexPart := strings.TrimPrefix(token, TokenPrefix)
	if len(hexPart) != TokenByteLen*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
