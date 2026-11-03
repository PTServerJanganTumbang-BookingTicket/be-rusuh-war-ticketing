package delivery

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/application/usecase"
	userhttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/http"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/nikverify"
	userpostgres "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/postgres"
	userredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/infrastructure/redis"
)

// Server holds the mux and the resources released on shutdown.
type Server struct {
	Handler *http.ServeMux

	pool  *pgxpool.Pool
	redis *goredis.Client
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

// New creates and wires the HTTP server with all dependencies.
// Dependency order: pool + redis → adapters → usecase → controller → routes.
func New(ctx context.Context, cfg config.Config) (*Server, error) {
	issuer, err := token.NewIssuer(cfg.Auth.JWTSecret, "rusuh-user", cfg.Auth.AccessTokenTTL)
	if err != nil {
		return nil, err
	}

	pool, err := userpostgres.NewPool(ctx, cfg.DB.DSN, cfg.DB.MaxConns, cfg.DB.ConnLifetime)
	if err != nil {
		return nil, err
	}

	redisClient, err := userredis.NewClient(ctx, cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		pool.Close()
		return nil, err
	}

	uc := usecase.NewUser(
		userpostgres.NewUserRepository(pool),
		userpostgres.NewEngagementRepository(pool),
		nikverify.NewClient(cfg.NikVerify.BaseURL, cfg.NikVerify.Timeout),
		userredis.NewRateLimiter(redisClient),
		issuer,
	)

	handler := userhttp.NewUser(uc, userhttp.Options{
		SecureCookies: cfg.Auth.SecureCookies,
		CookieDomain:  cfg.Auth.CookieDomain,
	})

	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	userhttp.RegisterRoutes(mux, handler)

	return &Server{Handler: mux, pool: pool, redis: redisClient}, nil
}
