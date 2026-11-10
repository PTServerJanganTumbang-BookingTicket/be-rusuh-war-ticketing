package specsuite

import (
	"strings"
	"testing"
)

func TestTagExpression(t *testing.T) {
	cases := []struct {
		name, tags, e2e string
		want, absent    []string
	}{
		{name: "default", want: []string{"~@draft", "~@e2e"}},
		{name: "step tags kept", tags: "@step-4 && ~@draft", want: []string{"@step-4 && ~@draft"}},
		{name: "e2e on", e2e: "1", absent: []string{"~@e2e"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GODOG_TAGS", c.tags)
			t.Setenv("REDIS_ADDR", "")
			t.Setenv("E2E", c.e2e)
			got := TagExpression()
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("TagExpression() = %q, want it to contain %q", got, w)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("TagExpression() = %q, must not contain %q", got, a)
				}
			}
			if !strings.Contains(got, "~@integration") {
				t.Errorf("TagExpression() = %q without REDIS_ADDR, want ~@integration", got)
			}
		})
	}
}

func TestIntegrationEnabled_followsRedisAddr(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	if IntegrationEnabled() {
		t.Error("IntegrationEnabled() with no REDIS_ADDR = true")
	}
	t.Setenv("REDIS_ADDR", "127.0.0.1:16379")
	t.Setenv("DATABASE_URL", "postgres://x")
	if IntegrationEnabled() == testing.Short() {
		t.Errorf("IntegrationEnabled() = %v under -short=%v", IntegrationEnabled(), testing.Short())
	}
	if RedisAddr() != "127.0.0.1:16379" || DatabaseURL() != "postgres://x" {
		t.Error("RedisAddr/DatabaseURL do not read the environment")
	}
}
