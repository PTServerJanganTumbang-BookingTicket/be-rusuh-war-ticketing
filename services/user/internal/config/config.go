package config

import "time"

// Config is the root configuration for the application.
type Config struct {
	App       App
	Log       Log
	DB        DB
	Redis     Redis
	NikVerify NikVerify
	Auth      Auth
}

// App holds general application settings.
//
// Note what is absent: no NIK HMAC key. Hashing happens in nik-verify, which
// returns the hash, so the pepper never enters this process (ADR-005 §8).
type App struct {
	Env     string // "local", "preprod", "production"
	Port    int
	BaseURL string // public base URL, used to build emailed links

	// MaxSavedNIKs is the per-account cap on saved friend-NIK entries (BR-12).
	// Configurable via MAX_SAVED_NIKS; defaults to 5 when unset.
	MaxSavedNIKs int
}

// Log holds configuration for structured logging.
type Log struct {
	Level  string
	Format string
}

// DB holds the shared Aurora connection settings.
type DB struct {
	DSN          string
	MaxConns     int32
	ConnLifetime time.Duration
}

// Redis holds the shared-state connection settings.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// NikVerify points at the nik-verify service.
type NikVerify struct {
	BaseURL string
	Timeout time.Duration
}

// Auth holds session settings.
type Auth struct {
	// JWTSecret signs access tokens. Shared with every service that verifies
	// them. Required, no default: a default signing key is a forged-token
	// generator. This is NOT the NIK pepper and must be a different secret —
	// this one can and should be rotated.
	JWTSecret string

	// SaleWindow is how long a sale stays open — gate opening to close.
	//
	// This service never queues anyone, so it would have no business knowing
	// this except for one thing: AccessTokenTTL is derived from it. The queue
	// owns the value (ADR-005 §4 uses it for acct:{ev}:{id} and token:{uuid}),
	// and it is still an open decision there — Bagian 17 #10. The default
	// below is a fallback, not a decision.
	SaleWindow time.Duration

	// AccessTokenTTL is how long an access token stays valid.
	//
	// Derived, not picked (ADR-005 §7 requires it), but derived from the
	// SESSION's interval rather than the journey's:
	//
	//	sale window     SALE_WINDOW_SECONDS   login → turn arrives
	//	journey + pause              990s     930 + 60 (BR-22 pause grace)
	//	margin                       150s     same absolute slack as the grant
	//	                ──────────────────
	//	                   = 22740s (6h19m) at the 21600s fallback
	//
	// The session starts at LOGIN, which happens before the queue. Contract
	// v2.3 set this to 1080s — correct for GRANT_TOKEN_TTL_SECONDS, which
	// bounds granted_at → payment deadline, and a coincidence for the session.
	// At ratePerMin 318 and QUEUE_CAPACITY 300000 the last position waits about
	// 15.7 hours, so an 18-minute session dies in the waiting room for most of
	// the queue.
	//
	// What breaks there is not the waiting room — the stream uses the cookie —
	// but GET /accounts/me/saved-niks on the Detail Ticket page. The saved-NIK
	// list 401s exactly when the customer opens it, forcing NIKs to be typed by
	// hand, forcing a Dukcapil call, and Dukcapil is fail-closed (BR-13). One
	// Dukcapil wobble then blocks a booking that would have passed on stored
	// evidence, inside a 300-second booking TTL.
	//
	// The price is written down in the contract §9: this is also the revocation
	// window, because the token is a stateless JWT and nothing is consulted
	// when it is verified. At 22740s the nik_verified claim is six hours of
	// cached AUTHORIZATION, not just identity. Pulling that claim out is the
	// change that should accompany this one, and it is open (ADR-005 Bagian 17
	// #11). The long-lived half of the session remains the refresh token —
	// stored, hashed, revocable, rotating.
	AccessTokenTTL time.Duration

	CookieDomain  string
	SecureCookies bool
}

// IsLocal reports whether this is a local development run.
func (a App) IsLocal() bool { return a.Env == "local" }
