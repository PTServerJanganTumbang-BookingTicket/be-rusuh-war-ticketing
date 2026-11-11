package seed

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckLocal refuses a database or Redis that is not on this machine, so a sample seed cannot reach a shared
// environment by accident. allowRemote (SEED_ALLOW_REMOTE=1) overrides it.
func CheckLocal(databaseURL, redisAddr string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	u, err := url.Parse(databaseURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("DATABASE_URL is not a URL with a host (set SEED_ALLOW_REMOTE=1 to skip this check)")
	}
	if !isLocalHost(u.Hostname()) {
		return fmt.Errorf("refusing to seed database host %q: not local (set SEED_ALLOW_REMOTE=1 to override)", u.Hostname())
	}
	host, _, err := net.SplitHostPort(redisAddr)
	if err != nil {
		host = redisAddr
	}
	if !isLocalHost(host) {
		return fmt.Errorf("refusing to seed redis host %q: not local (set SEED_ALLOW_REMOTE=1 to override)", host)
	}
	return nil
}

func isLocalHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
