// Package delivery wires the adapters and exposes the HTTP mux.
package delivery

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	paymentcfg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/application/usecase"
	paymenthttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/infrastructure/http"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/infrastructure/midtrans"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/infrastructure/postgres"
	paymentredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/infrastructure/redis"
)

// Server holds the wired dependencies.
type Server struct {
	// Handler is the mux, named to match the other services so cmd/ stays
	// interchangeable between them.
	Handler http.Handler
	close   func()
}

// Close releases the pools.
func (s *Server) Close() {
	if s.close != nil {
		s.close()
	}
}

// New wires everything and returns a ready mux.
func New(ctx context.Context, cfg paymentcfg.Config) (*Server, error) {
	pool, err := pgutil.NewPool(ctx, cfg.DB.DSN, cfg.DB.MaxConns, cfg.DB.ConnLifetime)
	if err != nil {
		return nil, err
	}

	rdb, err := paymentredis.NewClient(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		pool.Close()
		return nil, err
	}

	return newMux(pool, rdb, cfg)
}

// newMux wires the adapters and mux from already-opened connections.
// Separating this from New lets tests inject miniredis and a lazy pool
// without dialling real infrastructure, covering the construction and
// routing paths independently of the connection paths.
func newMux(pool *pgxpool.Pool, rdb *goredis.Client, cfg paymentcfg.Config) (*Server, error) {
	repo := postgres.NewBookingRepository(pool, idgen.New, newQRNonce)
	gateway := midtrans.New(cfg.Midtrans.BaseURL, cfg.Midtrans.ServerKey, cfg.Midtrans.Timeout)
	breaker := paymentredis.NewCircuitBreaker(rdb, cfg.Midtrans.BreakerThreshold, cfg.Midtrans.BreakerOpenFor)
	credentials := paymentredis.NewCredentialStore(rdb)

	uc := usecase.New(repo, gateway, breaker, credentials, usecase.Config{
		IconBase:            cfg.Payment.IconBase,
		PaymentTTL:          cfg.Payment.TTL,
		MinRetryRemaining:   cfg.Payment.MinRetryRemaining,
		MaxPaymentAttempts:  cfg.Payment.MaxAttempts,
		SettledTombstoneTTL: cfg.Payment.SettledTombstoneTTL,
		ServerKey:           cfg.Midtrans.ServerKey,
	})

	// Verify-only: payment-api never issues a session, so the TTL here is
	// irrelevant and only has to be positive. Same construction as queue's.
	sessions, err := token.NewIssuer(cfg.App.JWTSecret, "rusuh-user", time.Hour)
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		_ = rdb.Close()
		return nil, err
	}

	// The shared guard, the same one booking mounts: X-Grant-Token, then the
	// guest device binding, then the account session (ADR-006 §8.9, §8.13,
	// §8.17). It reads grant:{token} itself through pkg/rediskeys.
	guard := grantauth.Middleware(rdb, grantauth.Config{
		DevicePepper: []byte(cfg.App.DevicePepper),
		Sessions:     sessions,
	})

	mux := http.NewServeMux()
	// /sys/ping, matching every other service and this stack's HttpApi route.
	// The CI smoke test curls it after a deploy, so a different path here would
	// report a healthy release as a failed one.
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	paymenthttp.RegisterRoutes(mux, paymenthttp.NewPayment(uc), guard)

	return &Server{Handler: mux, close: func() {
		if pool != nil {
			pool.Close()
		}
		_ = rdb.Close()
	}}, nil
}

// newQRNonce mints tickets.qr_nonce.
//
// crypto/rand, 256 bits, and deliberately NOT derived from the ticket id or
// the NIK: a QR is screenshotted, forwarded and photographed, so it must be
// unguessable and must carry no identity (ADR-005 §8).
func newQRNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
