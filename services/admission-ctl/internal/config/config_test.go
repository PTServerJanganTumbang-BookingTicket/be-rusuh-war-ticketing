package config

import (
	"strings"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
)

func valid() Config {
	t := usecase.DefaultConfig()
	return Config{DatabaseURL: "postgres://x", RedisAddr: "r:6379", EventRefresh: 30 * time.Second, Tick: t, Reconcile: usecase.DefaultReconcileConfig()}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"no database":          func(c *Config) { c.DatabaseURL = "" },
		"no redis":             func(c *Config) { c.RedisAddr = "" },
		"refresh too fast":     func(c *Config) { c.EventRefresh = time.Millisecond },
		"tick too fast":        func(c *Config) { c.Tick.Interval = time.Millisecond },
		"no slots":             func(c *Config) { c.Tick.MaxSlots = 0 },
		"grant outlives never": func(c *Config) { c.Tick.GrantTTL = c.Tick.BookingTTL - time.Second },
		"no seed minutes":      func(c *Config) { c.Tick.EtaSeedMinutes = 0 },
	}
	for name, mutate := range cases {
		c := valid()
		mutate(&c)
		if c.Validate() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLoadDoesNotNeedEventIDs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "r:6379")
	c, err := Load()
	if err != nil {
		t.Fatalf("events now come from Redis, but Load failed: %v", err)
	}
	if c.EventRefresh != 30*time.Second {
		t.Fatalf("EventRefresh = %s, want 30s", c.EventRefresh)
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "r:6379")
	t.Setenv("MAX_CONCURRENT_SLOTS", "7")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Tick.MaxSlots != 7 || c.Tick.GrantTTL != 1080*time.Second {
		t.Fatalf("unexpected config: %+v", c.Tick)
	}
}

func TestReconcileDefaultsMatchThePlan(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "r:6379")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := usecase.ReconcileConfig{Every: 60 * time.Second, Threshold: 1, MaxCorrection: 20, LockTTL: 6 * time.Hour}
	if c.Reconcile != want {
		t.Fatalf("reconcile = %+v, want %+v (docs/05-booking/04-stock-reconciliation.md §6)", c.Reconcile, want)
	}
}

func TestReconcileReadsItsEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "r:6379")
	t.Setenv("STOCK_RECONCILE_SECONDS", "30")
	t.Setenv("STOCK_DRIFT_THRESHOLD", "2")
	t.Setenv("STOCK_MAX_CORRECTION", "50")
	t.Setenv("SALE_WINDOW_SECONDS", "7200")
	c, err := Load()
	want := usecase.ReconcileConfig{Every: 30 * time.Second, Threshold: 2, MaxCorrection: 50, LockTTL: 2 * time.Hour}
	if err != nil || c.Reconcile != want {
		t.Fatalf("reconcile = %+v (%v), want %+v", c.Reconcile, err, want)
	}
}

// 0 turns the job off; anything else must make sense, or a typo would silently repair nothing or everything.
func TestReconcileSettingsAreValidated(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_ADDR", "r:6379")
	t.Setenv("STOCK_RECONCILE_SECONDS", "0")
	if c, err := Load(); err != nil || c.Reconcile.Every != 0 {
		t.Fatalf("0 must turn the job off: %+v (%v)", c.Reconcile, err)
	}
	for name, env := range map[string][2]string{
		"a threshold below one":         {"STOCK_DRIFT_THRESHOLD", "0"},
		"a maximum below the threshold": {"STOCK_MAX_CORRECTION", "0"},
		"a lock without an expiry":      {"SALE_WINDOW_SECONDS", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("STOCK_RECONCILE_SECONDS", "60")
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), env[0]) {
				t.Fatalf("err = %v, want %s reported", err, env[0])
			}
		})
	}
}
