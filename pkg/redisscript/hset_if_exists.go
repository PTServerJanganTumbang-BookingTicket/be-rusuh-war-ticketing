package redisscript

import (
	"context"
	_ "embed"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

//go:embed hset_if_exists.lua
var hsetIfExistsSrc string

var hsetIfExists = goredis.NewScript(hsetIfExistsSrc)

// HSetIfExists sets fields (field, value, ...) on key only if the hash exists, and says whether it wrote.
// go-redis calls the script by SHA and loads it again on NOSCRIPT.
func HSetIfExists(ctx context.Context, rdb goredis.Scripter, key string, fieldValues ...any) (bool, error) {
	if len(fieldValues) == 0 || len(fieldValues)%2 != 0 {
		return false, fmt.Errorf("redisscript: HSetIfExists needs field/value pairs, got %d arguments", len(fieldValues))
	}
	n, err := hsetIfExists.Run(ctx, rdb, []string{key}, fieldValues...).Int()
	if err != nil {
		return false, fmt.Errorf("redisscript: hset_if_exists %s: %w", key, err)
	}
	return n == 1, nil
}
