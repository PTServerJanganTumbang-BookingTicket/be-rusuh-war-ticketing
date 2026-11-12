// Package delivery wires queue-service: clients → adapters → use case → routes.
package delivery

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/emf"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/idgen"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/ratelimit"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/application/usecase"
	queuehttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/http"
	queuepg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/postgres"
	queueredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/infrastructure/turnstile"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue/internal/queue/port/outbound"
)

// Server holds the mux and the resources released on shutdown.
type Server struct {
	Handler http.Handler
	pool    *pgxpool.Pool
	redis   goredis.UniversalClient
}

// Close releases the pool and the Redis client.
func (s *Server) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
	if s.redis != nil {
		_ = s.redis.Close()
	}
}

// NewRedis opens Redis: a cluster client in AWS, a plain client otherwise.
func NewRedis(ctx context.Context, cfg config.Config) (goredis.UniversalClient, error) {
	var rdb goredis.UniversalClient
	if cfg.RedisCluster {
		rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{cfg.RedisAddr}, Password: cfg.RedisPass})
	} else {
		rdb = goredis.NewClient(&goredis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPass})
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping %s: %w", cfg.RedisAddr, err)
	}
	return rdb, nil
}

// Clients are the expensive shared resources, opened once per container.
type Clients struct {
	Redis   goredis.UniversalClient
	DB      queuepg.Querier
	Pool    *pgxpool.Pool // closed by Server.Close when set
	Metrics outbound.Metrics
	Captcha outbound.Captcha // overrides the configured provider (tests)
	Clock   func() time.Time
}

// New opens Redis and the writer pool, then wires the handler.
func New(ctx context.Context, cfg config.Config) (*Server, error) {
	rdb, err := NewRedis(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pool, err := queuepg.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBLifetime)
	if err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return Wire(cfg, Clients{Redis: rdb, DB: pool, Pool: pool})
}

// Wire builds the handler from already-open clients.
func Wire(cfg config.Config, c Clients) (*Server, error) {
	issuer, err := token.NewIssuer(cfg.JWTSecret, "rusuh-user", time.Hour)
	if err != nil {
		return nil, err
	}
	if c.Metrics == nil {
		c.Metrics = emf.New("Rusuh/Queue", os.Stdout)
	}
	captcha := c.Captcha
	if captcha == nil {
		if cfg.Captcha == config.CaptchaMock {
			captcha = turnstile.Mock{}
		} else {
			captcha = turnstile.New(cfg.TurnstileURL, cfg.TurnstileSecret, cfg.TurnstileWait, c.Metrics)
		}
	}
	var bookings outbound.BookingReader
	var details outbound.BookingDetails
	if c.DB != nil {
		pg := queuepg.NewBookings(c.DB)
		bookings, details = pg, pg
	}
	join := usecase.NewJoin(cfg.Join, usecase.Deps{
		Store:    queueredis.NewStore(c.Redis),
		Bookings: bookings,
		Details:  details,
		Captcha:  captcha,
		Limiter:  ratelimit.New(c.Redis),
		Tokens:   issuer,
		Metrics:  c.Metrics,
		Clock:    c.Clock,
		NewToken: func() string { return idgen.NewPrefixed("qt_") },
	})
	me := usecase.NewMe(cfg.Join, usecase.Deps{
		Store:   queueredis.NewStore(c.Redis),
		Details: details,
		Tokens:  issuer,
		Metrics: c.Metrics,
		Clock:   c.Clock,
	})
	h := queuehttp.NewHandler(join, queuehttp.Options{
		CookieDomain: cfg.CookieDomain,
		CookieMaxAge: cfg.Join.SaleWindow,
		SSEBaseURL:   cfg.SSEBaseURL,
	})

	if details != nil { // /queue/me needs Aurora for customers who already have a grant
		h.WithMe(me)
	}
	h.WithServing(usecase.NewGetServing(queueredis.NewStore(c.Redis), c.Clock)) // /queue/serving: Redis only, no DB

	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	queuehttp.RegisterRoutes(mux, h)
	return &Server{Handler: mux, pool: c.Pool, redis: c.Redis}, nil
}
