package token

import (
	"strings"
	"testing"
)

const secretA = "secret-a"
const secretB = "secret-b"

// The whole point of the scheme: the same secret and the same application id
// always produce the same token, so a token minted once keeps working.
func TestForIsDeterministic(t *testing.T) {
	first := For(secretA, "myapp")
	for i := 0; i < 5; i++ {
		if got := For(secretA, "myapp"); got != first {
			t.Fatalf("For() is not deterministic: %q then %q", first, got)
		}
	}
	if len(first) != TokenLength {
		t.Errorf("token length = %d, want %d", len(first), TokenLength)
	}
	if strings.ToLower(first) != first {
		t.Errorf("token %q is not lower case", first)
	}
}

func TestForVariesWithAppAndSecret(t *testing.T) {
	seen := map[string]string{}
	for _, c := range []struct{ secret, app string }{
		{secretA, "myapp"},
		{secretA, "myapp2"},
		{secretA, "MyApp"},
		{secretB, "myapp"},
	} {
		tok := For(c.secret, c.app)
		key := c.secret + "/" + c.app
		if prev, clash := seen[tok]; clash {
			t.Errorf("%s and %s produce the same token %q", key, prev, tok)
		}
		seen[tok] = key
	}
}

// A different secret must not validate a token minted under another one; this
// is what makes publishing the source harmless.
func TestMatches(t *testing.T) {
	tok := For(secretA, "myapp")
	if !Matches(secretA, "myapp", tok) {
		t.Error("the correct token was rejected")
	}
	if Matches(secretB, "myapp", tok) {
		t.Error("a token minted under another secret was accepted")
	}
	if Matches(secretA, "otherapp", tok) {
		t.Error("a token was accepted for the wrong application")
	}
	if Matches(secretA, "myapp", strings.ToUpper(tok)) {
		t.Error("an upper-cased token was accepted; comparison should be exact")
	}
	if Matches(secretA, "myapp", "") {
		t.Error("an empty token was accepted")
	}
}

func TestNewSecret(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two generated secrets are identical")
	}
	if len(a) < 40 {
		t.Errorf("secret %q looks too short (%d chars)", a, len(a))
	}
	if strings.ToLower(a) != a {
		t.Errorf("secret %q is not lower case", a)
	}
}
