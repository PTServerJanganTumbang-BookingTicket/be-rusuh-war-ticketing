package token

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIssuer(t *testing.T) *Issuer {
	t.Helper()
	i, err := NewIssuer("test-signing-secret", "rusuh-user", 5*time.Minute)
	require.NoError(t, err)
	return i
}

func TestNewIssuer_RejectsEmptySecret(t *testing.T) {
	_, err := NewIssuer("", "rusuh-user", time.Minute)
	assert.Error(t, err)
}

func TestNewIssuer_RejectsNonPositiveTTL(t *testing.T) {
	_, err := NewIssuer("s", "rusuh-user", 0)
	assert.Error(t, err)
}

func TestIssueAndVerify_RoundTrip(t *testing.T) {
	i := newIssuer(t)
	raw, issued, err := i.Issue("USR-001", "a@example.com", true)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, i.TTL())

	claims, err := i.Verify(raw)
	require.NoError(t, err)
	assert.Equal(t, "USR-001", claims.Subject)
	assert.Equal(t, "a@example.com", claims.Email)
	assert.Equal(t, "rusuh-user", claims.Issuer)
	assert.True(t, claims.NIKVerified)
	assert.Equal(t, issued.ExpiresAt, claims.ExpiresAt)
}

func TestVerify_ExpiredToken(t *testing.T) {
	i := newIssuer(t)
	raw, _, err := i.Issue("USR-001", "a@example.com", false)
	require.NoError(t, err)

	i.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	_, err = i.Verify(raw)
	assert.ErrorIs(t, err, ErrExpired)
}

func TestVerify_WrongSecret(t *testing.T) {
	i := newIssuer(t)
	raw, _, err := i.Issue("USR-001", "a@example.com", false)
	require.NoError(t, err)

	other, err := NewIssuer("a-different-secret", "rusuh-user", 5*time.Minute)
	require.NoError(t, err)
	_, err = other.Verify(raw)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestVerify_TamperedPayload(t *testing.T) {
	i := newIssuer(t)
	raw, _, err := i.Issue("USR-001", "a@example.com", false)
	require.NoError(t, err)

	parts := strings.Split(raw, ".")
	var claims Claims
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(payload, &claims))
	claims.Subject = "USR-999" // escalate to someone else's account
	claims.NIKVerified = true
	repacked, err := json.Marshal(claims)
	require.NoError(t, err)
	parts[1] = base64.RawURLEncoding.EncodeToString(repacked)

	_, err = i.Verify(strings.Join(parts, "."))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestVerify_RejectsAlgNone(t *testing.T) {
	// The classic JWT attack: swap the algorithm for "none" and drop the
	// signature. The alg check must reject this before anything else runs.
	i := newIssuer(t)
	h, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	require.NoError(t, err)
	c, err := json.Marshal(Claims{Subject: "USR-999", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	forged := base64.RawURLEncoding.EncodeToString(h) + "." +
		base64.RawURLEncoding.EncodeToString(c) + "."

	_, err = i.Verify(forged)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestVerify_MalformedInputs(t *testing.T) {
	i := newIssuer(t)
	for _, raw := range []string{
		"",
		"not-a-token",
		"only.two",
		"a.b.c.d",
		"!!!.###.$$$",
	} {
		_, err := i.Verify(raw)
		assert.ErrorIs(t, err, ErrInvalid, "input %q", raw)
	}
}

func TestVerify_RejectsClaimsWithoutSubject(t *testing.T) {
	i := newIssuer(t)
	h, err := json.Marshal(header{Alg: algHS256, Typ: typJWT})
	require.NoError(t, err)
	c, err := json.Marshal(Claims{ExpiresAt: time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	signingInput := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	raw := signingInput + "." + base64.RawURLEncoding.EncodeToString(i.sign(signingInput))

	_, err = i.Verify(raw)
	assert.ErrorIs(t, err, ErrInvalid)
}
