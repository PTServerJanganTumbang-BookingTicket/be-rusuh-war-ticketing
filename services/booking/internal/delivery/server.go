// Package delivery is the composition root: it opens the connections and mounts the routes.
package delivery

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
)

// Deps are the connections the handler needs. Tests pass miniredis and a pgtest pool.
type Deps struct {
	Redis goredis.UniversalClient
	DB    *pgxpool.Pool // Aurora writer
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

// New opens the Aurora writer pool and the Redis client from cfg and wires the handler.
// The caller closes the returned Deps on shutdown.
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

// NewHandler wires the routes on the given connections. Every booking route will sit behind
// pkg/grantauth.Middleware (docs/05-booking/README.md §3); the routes arrive with build steps B2
// (GET /events/{event_id}/tickets) and B3 (GET /bookings/{booking_id}), POST /bookings with B4.
func NewHandler(_ config.Config, _ Deps) http.Handler {
	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")
	return noStore(mux)
}

// noStore marks every response uncacheable: a grant-bound answer must never be served from a CDN or browser cache.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
