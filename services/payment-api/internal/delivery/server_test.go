package delivery

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentcfg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/config"
)

func TestServer_Close_NilFields(t *testing.T) {
	s := &Server{}
	assert.NotPanics(t, s.Close)
}

func TestServer_Close_RunsTheCloser(t *testing.T) {
	called := 0
	s := &Server{close: func() { called++ }}

	s.Close()
	assert.Equal(t, 1, called)
}

// An unreachable database must fail at wiring time rather than at the first
// request: a Lambda that starts healthy against a dead Aurora serves 500s
// while reporting itself fine.
func TestNew_UnreachableDatabase_Fails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	srv, err := New(ctx, paymentcfg.Config{
		DB: paymentcfg.DB{
			// Port 1 is never a Postgres. A malformed DSN would also do, but
			// this exercises the dial path rather than the parser.
			DSN:      "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
			MaxConns: 1,
		},
		Redis: paymentcfg.Redis{Addr: "127.0.0.1:1"},
	})
	require.Error(t, err)
	assert.Nil(t, srv)
}

// 256 bits from crypto/rand, base64url so it survives a URL and a QR without
// escaping.
func TestNewQRNonce(t *testing.T) {
	got, err := newQRNonce()
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(got)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

// The nonce is the only thing standing between a screenshot and a forged
// ticket, so two tickets must never share one.
func TestNewQRNonce_IsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 256; i++ {
		got, err := newQRNonce()
		require.NoError(t, err)
		_, dup := seen[got]
		require.False(t, dup, "nonce repeated")
		seen[got] = struct{}{}
	}
}

// Deliberately NOT derived from the ticket id, the booking id or the NIK: a QR
// is screenshotted, forwarded and photographed, so it must be unguessable and
// must carry no identity.
func TestNewQRNonce_CarriesNoIdentity(t *testing.T) {
	got, err := newQRNonce()
	require.NoError(t, err)

	for _, identity := range []string{"RB-", "3171", "ACC-", "@"} {
		assert.NotContains(t, got, identity)
	}
}
