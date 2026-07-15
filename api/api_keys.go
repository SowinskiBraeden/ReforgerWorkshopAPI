package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

type GeneratedAPIKey struct {
	Raw      string
	Hash     string
	Prefix   string
	LastFour string
}

func GenerateAPIKey(mode string, hashSecret string) (GeneratedAPIKey, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "live" {
		mode = "test"
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return GeneratedAPIKey{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	raw := "rfm_" + mode + "_" + token
	hash, err := HashAPIKey(raw, hashSecret)
	if err != nil {
		return GeneratedAPIKey{}, err
	}
	prefixLen := 13
	if len(raw) < prefixLen {
		prefixLen = len(raw)
	}
	return GeneratedAPIKey{
		Raw:      raw,
		Hash:     hash,
		Prefix:   raw[:prefixLen],
		LastFour: raw[len(raw)-4:],
	}, nil
}

func HashAPIKey(raw string, hashSecret string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("api key is empty")
	}
	secret := strings.TrimSpace(hashSecret)
	if secret == "" {
		sum := sha256.Sum256([]byte(raw))
		return hex.EncodeToString(sum[:]), nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func VerifyAPIKeyHash(raw string, hash string, hashSecret string) bool {
	candidate, err := HashAPIKey(raw, hashSecret)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(candidate), []byte(hash))
}
