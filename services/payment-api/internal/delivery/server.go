// Package delivery wires the adapters and exposes the HTTP mux.
package delivery

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/emf"
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
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Server holds the wired dependencies.
type Server struct {
	// Handler is the mux, named to match the other services so cmd/ stays
	// interchangeable between them.
	Handler http.Handler
	// events is the Lambda event handler, supporting both API Gateway and
	// internal ops like sweep_awaiting_payment.
	events *EventHandler
	close  func()
}

// Close releases the pools.
func (s *Server) Close() {
	if s.close != nil {
		s.close()
	}
}

// Events returns the Lambda event handler that routes both API Gateway
// requests and internal operations.
func (s *Server) Events() *EventHandler {
	return s.events
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
	holds := paymentredis.NewHolds(rdb)
	// Cached, because §5.1 is on the path of every customer on the payment
	// page. The TTL is how long a switch takes to bite: seconds, because the
	// lever exists to replace a fifteen-minute deploy, not to be synchronous.
	switches := postgres.NewSwitchRepository(pool, cfg.Payment.SwitchCacheTTL, nil)

	// Metrics go to stdout as CloudWatch EMF lines (docs/metrics.md).
	metrics := emf.New("Rusuh/Payment", os.Stdout)
	uc := usecase.New(repo, gateway, breaker, credentials, switches, metrics, usecase.Config{
		IconBase:            cfg.Payment.IconBase,
		PaymentTTL:          cfg.Payment.TTL,
		MinRetryRemaining:   cfg.Payment.MinRetryRemaining,
		MaxPaymentAttempts:  cfg.Payment.MaxAttempts,
		SettledTombstoneTTL: cfg.Payment.SettledTombstoneTTL,
		SignatureSecretKey:  cfg.Midtrans.SignatureSecretKey,
	}).(*usecase.PaymentUsecase).WithHolds(holds)

	// The sweep use case shares adapters with the payment use case but has its
	// own configuration. The repo satisfies ISweepRepository because it
	// implements DueAwaitingPayment (migration 0019).
	sweepRepo := repo.(portout.ISweepRepository)
	sweep := usecase.NewSweep(sweepRepo, gateway, breaker, credentials, holds, usecase.SweepConfig{
		GraceSeconds:        cfg.Sweep.GraceSeconds,
		BatchSize:           cfg.Sweep.BatchSize,
		StuckMinutes:        cfg.Sweep.StuckMinutes,
		MaxPaymentAttempts:  cfg.Payment.MaxAttempts,
		SettledTombstoneTTL: cfg.Payment.SettledTombstoneTTL,
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
	guardCfg := grantauth.Config{
		DevicePepper: []byte(cfg.App.DevicePepper),
		// main's grantauth takes a TokenVerifier interface; *token.Issuer
		// satisfies it with Verify(string) (token.Claims, error).
		Tokens: sessions,
	}
	// §5.1–§5.4 also accept the WebView's X-Payment-Session (ADR-007); minting
	// a handoff code does not, so only the app can start a WebView payment.
	sessionGuard := grantauth.MiddlewareWithSession(rdb, guardCfg)
	grantGuard := grantauth.Middleware(rdb, guardCfg)
	handoff := usecase.NewHandoff(repo, paymentredis.NewHandoffStore(rdb), paymentredis.NewHandoffLimiter(rdb),
		cfg.App.PaymentPage())

	mux := http.NewServeMux()
	// /sys/ping, matching every other service and this stack's HttpApi route.
	// The CI smoke test curls it after a deploy, so a different path here would
	// report a healthy release as a failed one.
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	paymenthttp.RegisterRoutes(mux, paymenthttp.NewPayment(uc), sessionGuard)
	paymenthttp.RegisterHandoffRoutes(mux, paymenthttp.NewHandoff(handoff), grantGuard)

	// The HTTP adapter for API Gateway events — same format Lambda uses.
	httpProxy := func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return httpadapter.NewV2(mux).ProxyWithContext(ctx, req)
	}

	return &Server{
		Handler: mux,
		events:  NewEventHandler(sweep, httpProxy),
		close: func() {
			if pool != nil {
				pool.Close()
			}
			_ = rdb.Close()
		},
	}, nil
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
