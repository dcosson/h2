package linearagent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifySignature reports whether sig (hex HMAC-SHA256 of body, keyed by
// secret) is valid. An empty secret disables verification (returns true) — the
// caller is responsible for warning in that case. Shared by the direct webhook
// receiver and the relay edge so there is one verification implementation.
func VerifySignature(secret, sig string, body []byte) bool {
	if secret == "" {
		return true
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
