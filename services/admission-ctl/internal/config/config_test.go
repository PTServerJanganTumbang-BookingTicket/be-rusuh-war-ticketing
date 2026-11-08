package config

import (
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
)

func valid() Config {
	t := usecase.DefaultConfig()
	return Config{DatabaseURL: "postgres://x", RedisAddr: "r:6379", EventRefresh: 30 * time.Second, Tick: t}
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
