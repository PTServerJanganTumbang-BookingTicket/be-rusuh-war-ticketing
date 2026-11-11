package delivery

import (
	"context"
	"net/http"

	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/infrastructure/dukcapil"
	nikhttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/infrastructure/http"
	nikpostgres "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/infrastructure/postgres"
	nikredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/infrastructure/redis"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/inbound"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the mux and the resources that must be released on shutdown.
type Server struct {
	Handler *http.ServeMux

	uc    inbound.INikUsecase
	pool  *pgxpool.Pool
	redis *goredis.Client
}

// Events is the Lambda entry point: API Gateway events go to Handler, the internal {"op":"validate"} event goes to the
// same use case. The local HTTP server (cmd/server) does not use it.
func (s *Server) Events() *EventHandler {
	return NewEventHandler(s.uc, httpadapter.NewV2(s.Handler).ProxyWithContext)
}

// Close releases the pool and Redis client.
func (s *Server) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
	if s.redis != nil {
		_ = s.redis.Close()
	}
}

// New creates and wires the HTTP server with all dependencies.
// Dependency order: pool + redis → adapters → usecase → controller → routes.
func New(ctx context.Context, cfg config.Config) (*Server, error) {
	pool, err := nikpostgres.NewPool(ctx, cfg.DB.DSN, cfg.DB.MaxConns, cfg.DB.ConnLifetime)
	if err != nil {
		return nil, err
	}

	redisClient, err := nikredis.NewClient(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		pool.Close()
		return nil, err
	}

	var dukcapilAdapter = dukcapil.NewClient(
		cfg.Dukcapil.BaseURL,
		cfg.Dukcapil.APIKey,
		cfg.Dukcapil.Timeout,
		cfg.Dukcapil.ExtraHeaders,
	)
	if cfg.Dukcapil.IsStub() {
		dukcapilAdapter = dukcapil.NewStubAdapter()
	}

	uc := usecase.NewNik(
		dukcapilAdapter,
		nikpostgres.NewEvidenceRepository(pool),
		nikredis.NewVerificationCache(redisClient),
		nikredis.NewRateLimiter(redisClient, cfg.Dukcapil.RateLimitRPS, cfg.Dukcapil.BurstCapacity),
		nikredis.NewCircuitBreaker(redisClient, cfg.Dukcapil.BreakerThreshold, cfg.Dukcapil.BreakerOpenFor),
		cfg.App.NIKPepper,
	)

	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	nikhttp.RegisterRoutes(mux, nikhttp.NewNik(uc))

	return &Server{Handler: mux, uc: uc, pool: pool, redis: redisClient}, nil
}
