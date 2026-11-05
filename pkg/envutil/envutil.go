// Package envutil reads typed values from environment variables.
//
// Every helper returns a fallback when the variable is absent or cannot be
// parsed, so callers that need to treat absence as an error can check the
// return against their fallback after calling.
package envutil

import (
	"os"
	"strconv"
	"time"
)

// Env returns the value of key, or fallback when the variable is unset or empty.
func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// EnvInt returns the integer value of key, or fallback when the variable is
// absent or not a valid integer.
func EnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

// EnvBool returns the boolean value of key, or fallback when the variable is
// absent or not a valid boolean (strconv.ParseBool rules).
func EnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// EnvFloat returns the float64 value of key, or fallback when absent or
// unparseable.
func EnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// EnvDuration reads a duration. Keys ending in _MS are interpreted as
// milliseconds; keys ending in _SECONDS as seconds. Any other key accepts a Go
// duration string such as "30m". Returns fallback when absent or unparseable.
func EnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		switch {
		case len(key) > 3 && key[len(key)-3:] == "_MS":
			return time.Duration(n) * time.Millisecond
		case len(key) > 8 && key[len(key)-8:] == "_SECONDS":
			return time.Duration(n) * time.Second
		default:
			return time.Duration(n) * time.Second
		}
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return fallback
}
