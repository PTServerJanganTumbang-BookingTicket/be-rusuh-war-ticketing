package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_defaults(t *testing.T) {
	t.Setenv("SSE_STALE_AFTER_SECONDS", "45")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Port != 8085 || cfg.Stream.Keepalive != 15*time.Second || cfg.Stream.StaleAfter != 45*time.Second ||
		cfg.Stream.IPLimit.Capacity != 5 || cfg.Stream.IPLimit.Refill != 5 || !cfg.RedisCluster {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestValidate(t *testing.T) {
	good := func() Config {
		c, _ := Load()
		return c
	}
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"no redis":          {func(c *Config) { c.RedisAddr = "" }, "REDIS_ADDR"},
		"keepalive too big": {func(c *Config) { c.Stream.Keepalive = 2 * time.Minute }, "KEEPALIVE"},
		"stale too short":   {func(c *Config) { c.Stream.StaleAfter = time.Second }, "STALE_AFTER"},
		"no drain":          {func(c *Config) { c.Stream.DrainDeadline = 0 }, "DRAIN"},
		"tiny buffer":       {func(c *Config) { c.Stream.ConnBuffer = 1 }, "CONN_BUFFER"},
		"no ip limit":       {func(c *Config) { c.Stream.IPLimit.Capacity = 0 }, "IP_LIMIT"},
	} {
		t.Run(name, func(t *testing.T) {
			c := good()
			tc.mutate(&c)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if err := good().Validate(); err != nil {
		t.Errorf("defaults rejected: %v", err)
	}
}
