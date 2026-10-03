// Package token derives the upload token of an application.
//
// The token is a pure function of the application id: the same id always yields
// the same token, on every deployment and in every environment. That is the
// point — an administrator mints a token once with `updatesite token <app-id>`
// and hands it to the application's developers, and it keeps working no matter
// where the site is deployed or how it is reconfigured.
//
// # Threat model
//
// Because the derivation key is compiled in, the token is a capability rather
// than a secret in the cryptographic sense: it cannot be guessed from another
// token, but anyone who has this binary (or its source) can mint the token for
// any application id. It protects against the realistic risks — a developer who
// only knows the site URL, a leaked URL, a scanning bot — and not against an
// attacker who already has the program. Treat the published image as trusted
// material, and do not expose the upload endpoint to the open internet if that
// is not acceptable to you.
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// derivationKey is deliberately compiled in. See the package comment: the
// requirement is that a token is identical across environments, so there is
// nowhere else it could live. Changing it invalidates every issued token.
var derivationKey = []byte("5379a34ec084a036555a1609a291aa43d5a6e1704476612c16ee503bf129b935")

// context separates this use of the key from any other use.
const context = "updatesite/v1/upload-token\x00"

// TokenLength is the number of base32 characters a token carries, giving 160
// bits of derived material.
const TokenLength = 32

// encoding omits padding and lower-cases the result so tokens are easy to read
// aloud and paste.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// For returns the upload token of an application id.
func For(appID string) string {
	mac := hmac.New(sha256.New, derivationKey)
	mac.Write([]byte(context))
	mac.Write([]byte(appID))
	sum := mac.Sum(nil)
	return strings.ToLower(encoding.EncodeToString(sum))[:TokenLength]
}

// Matches reports whether candidate is the token of appID. The comparison is
// constant time.
func Matches(appID, candidate string) bool {
	return hmac.Equal([]byte(For(appID)), []byte(candidate))
}
