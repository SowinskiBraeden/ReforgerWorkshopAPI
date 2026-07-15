package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Account session tokens are stateless signed cookies: base64(accountID.expiry)
// plus an HMAC over that payload. Login tokens are single-use random secrets
// stored hashed in the billing store.

func CreateAccountSessionToken(accountID string, expires time.Time, secret string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(accountID)) + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + signSessionPayload(payload, secret)
}

func VerifyAccountSessionToken(token string, secret string, now time.Time) (string, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", false
	}
	payload := parts[0] + "." + parts[1]
	expected := signSessionPayload(payload, secret)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", false
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.After(time.Unix(expiresUnix, 0)) {
		return "", false
	}
	accountID, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(accountID) == 0 {
		return "", false
	}
	return string(accountID), true
}

func signSessionPayload(payload string, secret string) string {
	mac := hmac.New(sha256.New, []byte("account-session:"+secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateLoginToken returns the raw single-use sign-in token and its hash for
// storage. Only the hash is persisted.
func GenerateLoginToken(hashSecret string) (raw string, hash string, err error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	raw = "lt_" + base64.RawURLEncoding.EncodeToString(random[:])
	hash, err = HashAPIKey(raw, hashSecret)
	return raw, hash, err
}
