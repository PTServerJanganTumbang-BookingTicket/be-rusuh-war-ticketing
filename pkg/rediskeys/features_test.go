package rediskeys_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
)

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

type world struct {
	key   string
	err   error
	slots map[string]int64 // key -> slot, for the event under test
	other map[string]int64 // keys of a second event
}

func initScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Step(`^I build the "([^"]*)" key for event "([^"]*)" with "([^"]*)"$`, w.buildEventKey)
	sc.Step(`^I build the "([^"]*)" key for event "([^"]*)"$`, func(b, ev string) error { return w.buildEventKey(b, ev, "") })
	sc.Step(`^I build the "([^"]*)" key with "([^"]*)"$`, w.buildPlainKey)
	sc.Step(`^the key is "([^"]*)"$`, w.keyIs)
	sc.Step(`^building fails with "([^"]*)"$`, w.buildingFails)
	sc.Step(`^the broadcast channel is "([^"]*)"$`, func(want string) error { return eq("broadcast channel", rediskeys.ChannelBroadcast, want) })
	sc.Step(`^the status channel is "([^"]*)"$`, func(want string) error { return eq("status channel", rediskeys.ChannelStatus, want) })
	sc.Step(`^pkg/rediskeys exports no builder named "([^"]*)"$`, exportsNoBuilder)
	sc.Step(`^pkg/rediskeys exports no channel named "([^"]*)"$`, exportsNoChannel)
	sc.Step(`^a Redis cluster with 3 masters$`, w.redisCluster)
	sc.Step(`^I compute CLUSTER KEYSLOT for every event key of "([^"]*)", including stock for "([^"]*)", "([^"]*)", "([^"]*)"$`, w.computeSlots)
	sc.Step(`^all of them return the same slot$`, w.sameSlot)
	sc.Step(`^the keys of event "([^"]*)" may return a different slot$`, w.otherEventSlots)
}

// eventKeys lists every key builder on Event, so the slot check cannot miss one.
func eventKeys(ev rediskeys.Event, cats ...string) []string {
	keys := []string{ev.QueueHash(), ev.QueueState(), ev.QueueIssued(), ev.ByTicket(),
		ev.FullBookedAt(), ev.Expiries(), ev.Acct("acc-1")}
	for _, c := range cats {
		keys = append(keys, ev.Stock(c))
	}
	return keys
}

func (w *world) buildEventKey(builder, eventID, arg string) error {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		w.err = err
		return nil
	}
	m := map[string]func() string{
		"QueueHash":    ev.QueueHash,
		"QueueState":   ev.QueueState,
		"QueueIssued":  ev.QueueIssued,
		"ByTicket":     ev.ByTicket,
		"FullBookedAt": ev.FullBookedAt,
		"Expiries":     ev.Expiries,
		"Stock":        func() string { return ev.Stock(arg) },
		"Acct":         func() string { return ev.Acct(arg) },
	}
	f, ok := m[builder]
	if !ok {
		return fmt.Errorf("unknown event key builder %q", builder)
	}
	w.key = f()
	return nil
}

func (w *world) buildPlainKey(builder, arg string) error {
	m := map[string]func(string) string{
		"Token":         rediskeys.Token,
		"Grant":         rediskeys.Grant,
		"JoinRateLimit": rediskeys.JoinRateLimit,
		"JoinAcctLimit": rediskeys.JoinAcctLimit,
		"SSERateLimit":  rediskeys.SSERateLimit,
	}
	f, ok := m[builder]
	if !ok {
		return fmt.Errorf("unknown key builder %q", builder)
	}
	w.key = f(arg)
	return nil
}

func (w *world) keyIs(want string) error {
	if w.err != nil {
		return fmt.Errorf("building failed: %w", w.err)
	}
	return eq("key", w.key, want)
}

func (w *world) buildingFails(msg string) error {
	if w.err == nil {
		return fmt.Errorf("building succeeded with %q, want error %q", w.key, msg)
	}
	if !errors.Is(w.err, rediskeys.ErrInvalidEventID) || !strings.Contains(w.err.Error(), msg) {
		return fmt.Errorf("error = %v, want %q", w.err, msg)
	}
	return nil
}

func eq(what, got, want string) error {
	if got != want {
		return fmt.Errorf("%s = %q, want %q", what, got, want)
	}
	return nil
}

// exported parses the package source (not tests) and returns exported
// function/method names and the values of exported string constants.
func exported() (names map[string]bool, constValues map[string]bool, err error) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		return nil, nil, err
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, f)
	}
	names, constValues = map[string]bool{}, map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, n := range vs.Names {
						if !n.IsExported() {
							continue
						}
						names[n.Name] = true
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								v, _ := strconv.Unquote(lit.Value)
								constValues[v] = true
							}
						}
					}
				}
			}
		}
	}
	return names, constValues, nil
}

func exportsNoBuilder(name string) error {
	names, _, err := exported()
	if err != nil {
		return err
	}
	if names[name] {
		return fmt.Errorf("pkg/rediskeys exports %s, removed by ADR-006", name)
	}
	return nil
}

func exportsNoChannel(name string) error {
	_, values, err := exported()
	if err != nil {
		return err
	}
	if values[name] {
		return fmt.Errorf("pkg/rediskeys exports channel %q, removed by ADR-006", name)
	}
	return nil
}

func (w *world) redisCluster() error {
	// KEYSLOT is a pure CRC16 function: the slot does not depend on how many
	// masters exist, so the single-node cluster from docker-compose is enough.
	// What matters is that cluster mode is on.
	rdb := goredis.NewClient(&goredis.Options{Addr: specsuite.RedisAddr()})
	defer func() { _ = rdb.Close() }()
	info, err := rdb.ClusterInfo(context.Background()).Result()
	if err != nil {
		return fmt.Errorf("CLUSTER INFO: %w (is Redis running in cluster mode? make deps-up)", err)
	}
	if !strings.Contains(info, "cluster_state:ok") {
		return fmt.Errorf("cluster not ready: %s", info)
	}
	return nil
}

func keySlots(keys []string) (map[string]int64, error) {
	rdb := goredis.NewClient(&goredis.Options{Addr: specsuite.RedisAddr()})
	defer func() { _ = rdb.Close() }()
	out := map[string]int64{}
	for _, k := range keys {
		if k == "" {
			return nil, errors.New("a key builder returned an empty key")
		}
		s, err := rdb.ClusterKeySlot(context.Background(), k).Result()
		if err != nil {
			return nil, fmt.Errorf("CLUSTER KEYSLOT %s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func (w *world) computeSlots(eventID, c1, c2, c3 string) error {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return err
	}
	w.slots, err = keySlots(eventKeys(ev, c1, c2, c3))
	return err
}

func sameSlot(slots map[string]int64) error {
	var first int64 = -1
	for k, s := range slots {
		if first == -1 {
			first = s
		} else if s != first {
			return fmt.Errorf("key %s is in slot %d, others in %d", k, s, first)
		}
	}
	return nil
}

func (w *world) sameSlot() error { return sameSlot(w.slots) }

// otherEventSlots: a second event may land anywhere, but its own keys must
// still share one slot.
func (w *world) otherEventSlots(eventID string) error {
	ev, err := rediskeys.NewEvent(eventID)
	if err != nil {
		return err
	}
	w.other, err = keySlots(eventKeys(ev, "VIP"))
	if err != nil {
		return err
	}
	return sameSlot(w.other)
}
