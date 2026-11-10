package rediskeys_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalogListsEveryBuilder keeps docs/redis-keys.md in step with the code:
// every exported key builder and channel constant must appear in it.
func TestCatalogListsEveryBuilder(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "redis-keys.md"))
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	names, _, err := exported()
	if err != nil {
		t.Fatal(err)
	}
	notKeys := map[string]bool{"NewEvent": true, "ID": true, "ErrInvalidEventID": true, "Event": true}
	for name := range names {
		if notKeys[name] {
			continue
		}
		if !strings.Contains(string(doc), "`"+name+"`") && !strings.Contains(string(doc), "`Event."+name+"`") {
			t.Errorf("docs/redis-keys.md does not list %s: add a row for it", name)
		}
	}
}
