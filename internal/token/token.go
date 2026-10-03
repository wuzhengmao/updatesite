// Package token derives the upload token of an application from a deployment
// secret.
//
// The derivation is a pure function of (secret, application id). With the same
// secret every deployment produces the same token for the same application, so
// an administrator still mints a token once and hands it to a developer, and it
// keeps working wherever the site runs. The secret is supplied by the
// deployment through UPLOAD_SECRET rather than compiled in, so publishing this
// source gives nobody the ability to mint tokens for a site they do not control.
//
// Uploading stays disabled until a secret is configured.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// context separates this use of the secret from any other use.
const context = "updatesite/v1/upload-token\x00"

const (
	// TokenLength is the number of base32 characters a token carries, giving
	// 160 bits of derived material.
	TokenLength = 32
	// SecretBytes is the entropy of a generated secret.
	SecretBytes = 32
)

// encoding omits padding and lower-cases the result so tokens are easy to read
// aloud and paste.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// For returns the upload token of an application under a deployment secret.
func For(secret, appID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(context))
	mac.Write([]byte(appID))
	return strings.ToLower(encoding.EncodeToString(mac.Sum(nil)))[:TokenLength]
}

// Matches reports whether candidate is the token of appID. The comparison is
// constant time.
func Matches(secret, appID, candidate string) bool {
	return hmac.Equal([]byte(For(secret, appID)), []byte(candidate))
}

// NewSecret returns a fresh random secret, for an administrator to paste into
// the deployment configuration.
func NewSecret() (string, error) {
	buf := make([]byte, SecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToLower(encoding.EncodeToString(buf)), nil
}
