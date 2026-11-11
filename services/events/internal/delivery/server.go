// Package delivery is the composition root: it opens the connections and mounts the routes.
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
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/application/usecase"
	eventhttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/infrastructure/http"
	eventpg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/infrastructure/postgres"
	eventredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/infrastructure/redis"
)

// Deps are the connections the handler needs. Tests pass miniredis and a pgtest pool.
type Deps struct {
	Redis goredis.UniversalClient
	DB    *pgxpool.Pool // Aurora reader
}

// Close releases the connections.
func (d Deps) Close() {
	if d.DB != nil {
		d.DB.Close()
	}
	if d.Redis != nil {
		_ = d.Redis.Close()
	}
}

// New opens the Aurora reader pool and the Redis client from cfg and wires the handler.
func New(ctx context.Context, cfg config.Config) (http.Handler, Deps, error) {
	var rdb goredis.UniversalClient
	if cfg.Redis.Cluster {
		rdb = goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{cfg.Redis.Addr}, Password: cfg.Redis.Password})
	} else {
		rdb = goredis.NewClient(&goredis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password})
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, Deps{}, fmt.Errorf("redis %s: %w", cfg.Redis.Addr, err)
	}
	pool, err := pgutil.NewPool(ctx, cfg.DB.URL, cfg.DB.MaxConns, 30*time.Minute)
	if err != nil {
		_ = rdb.Close()
		return nil, Deps{}, fmt.Errorf("database: %w", err)
	}
	d := Deps{Redis: rdb, DB: pool}
	return NewHandler(cfg, d), d, nil
}

// NewHandler wires the two public routes. Both are cached by the CDN, so each handler sets its own Cache-Control.
func NewHandler(cfg config.Config, d Deps) http.Handler {
	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")

	deps := usecase.Deps{
		Events:  eventpg.NewStore(d.DB),
		Live:    eventredis.NewLive(d.Redis),
		Metrics: emf.New("Rusuh/Events", os.Stdout),
		Clock:   time.Now,
	}
	ucfg := usecase.Config{
		QueueCapacity:  cfg.Events.QueueCapacity,
		CaptchaSiteKey: cfg.Events.TurnstileSiteKey,
	}
	caching := eventhttp.Caching{MaxAge: cfg.Events.CacheMaxAgeSeconds, DegradedMaxAge: cfg.Events.DegradedMaxAge}
	mux.Handle("GET /events", eventhttp.NewListEvents(usecase.NewList(deps, ucfg), caching))
	mux.Handle("GET /events/{event_id}", eventhttp.NewGetEvent(usecase.NewGet(deps, ucfg), caching))
	return mux
}
