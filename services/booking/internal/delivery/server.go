// Package delivery is the composition root: it opens the connections and mounts the routes.
package delivery

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/emf"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/eticket"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgutil"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	bookinghttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/http"
	bookingpg "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/postgres"
	bookingredis "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/infrastructure/redis"
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
	// Check the configuration that needs no network first, so a bad key is reported as such, not as a connection error.
	if _, err := ticketLinks(cfg); err != nil {
		return nil, Deps{}, err
	}
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
	h, err := NewHandler(cfg, d)
	if err != nil {
		d.Close()
		return nil, Deps{}, err
	}
	return h, d, nil
}

// NewHandler wires the routes on the given connections. Every booking route sits behind pkg/grantauth.Middleware
// (docs/05-booking/README.md §3). POST /bookings arrives with build step B4.
func NewHandler(cfg config.Config, d Deps) (http.Handler, error) {
	links, err := ticketLinks(cfg)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	apinizer.RegisterHealthCheck(mux, "/sys/ping")

	// The session verifier is what §8.17 added: a grant naming an account is
	// only honoured alongside that account's own Bearer token, so a leaked
	// grant cannot be spent in someone else's name. Built with the same
	// issuer name and TTL user-service signs with.
	//
	// No secret means no verifier, and the guard then answers 503 for an
	// account grant rather than waving it through. config.validate() already
	// requires the secret outside local, so that degraded state is reachable
	// only on a developer's machine - where guest flows still work and a
	// logged-in call tells you plainly what is missing.
	var sessions *token.Issuer
	if cfg.Grant.JWTSecret != "" {
		sessions, err = token.NewIssuer(cfg.Grant.JWTSecret, "rusuh-user", time.Hour)
		if err != nil {
			return nil, fmt.Errorf("session verifier: %w", err)
		}
	} else {
		slog.Warn("no JWT_SECRET: account grants will be refused with 503 (ADR-006 §8.17)")
	}
	grant := grantauth.Middleware(d.Redis, grantauth.Config{
		DevicePepper: cfg.Grant.DevicePepper,
		Sessions:     sessions,
	})
	metrics := emf.New("Rusuh/Booking", os.Stdout)
	store := bookingpg.NewStore(d.DB)
	ticketPage := usecase.NewTicketPage(usecase.TicketPageDeps{
		Bookings: store,
		Catalog:  store,
		Stock:    bookingredis.NewStock(d.Redis),
		Metrics:  metrics,
		Clock:    time.Now,
	}, cfg.Booking.MaxTicketsPerTransaction)
	mux.Handle("GET /events/{event_id}/tickets", grant(bookinghttp.NewTicketPage(ticketPage)))

	detail := usecase.NewBookingDetail(usecase.BookingDetailDeps{Details: store, Links: links, Metrics: metrics, Clock: time.Now})
	mux.Handle("GET /bookings/{booking_id}", grant(bookinghttp.NewBookingDetail(detail)))

	return noStore(mux), nil
}

// ticketLinks builds the e-ticket signer. Outside local the key is required by config; locally an empty key gets a
// random one, so links work until the next restart (they would not verify in user-service anyway).
func ticketLinks(cfg config.Config) (*eticket.Signer, error) {
	key := cfg.Booking.ETicketSigningKey
	if len(key) == 0 && cfg.App.Env == "local" {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("local e-ticket key: %w", err)
		}
	}
	s, err := eticket.NewSigner(key)
	if err != nil {
		return nil, fmt.Errorf("ETICKET_SIGNING_KEY: %w", err)
	}
	return s, nil
}

// noStore marks every response uncacheable: a grant-bound answer must never be served from a CDN or browser cache.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
