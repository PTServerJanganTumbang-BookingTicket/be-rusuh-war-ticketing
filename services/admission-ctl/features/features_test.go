package features

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/specsuite"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
)

func TestMain(m *testing.M) {
	code := m.Run()
	dropDatabases()
	os.Exit(code)
}

func TestFeatures(t *testing.T) { specsuite.Run(t, initScenario) }

// ---------------------------------------------------------------- databases

var (
	dbMu    sync.Mutex
	created []string // throwaway database names, dropped in TestMain
	shared  *testDB
)

type testDB struct {
	name   string
	admin  *pgxpool.Pool // seeding and assertions
	writer *pgxpool.Pool // what the store uses, traced
	reader *pgxpool.Pool // stands for a read replica: the store must never use it
	wTrace *sqlTracer
	rTrace *sqlTracer
}

type sqlTracer struct {
	mu   sync.Mutex
	sqls []string
}

func (t *sqlTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	t.sqls = append(t.sqls, d.SQL)
	t.mu.Unlock()
	return ctx
}
func (t *sqlTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *sqlTracer) reset() { t.mu.Lock(); t.sqls = nil; t.mu.Unlock() }
func (t *sqlTracer) saw(sub string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.sqls {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
func (t *sqlTracer) count() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.sqls) }

func migrationFiles(suffix string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*."+suffix+".sql"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no %s migrations found: %v", suffix, err)
	}
	sort.Strings(files)
	return files, nil
}

func applyAll(ctx context.Context, db *pgxpool.Pool, suffix string, reverse bool) error {
	files, err := migrationFiles(suffix)
	if err != nil {
		return err
	}
	if reverse {
		for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
			files[i], files[j] = files[j], files[i]
		}
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// newDatabase creates a throwaway database. Real migrations are applied when migrate is true.
func newDatabase(ctx context.Context, migrate bool) (*testDB, error) {
	server := specsuite.DatabaseURL()
	if server == "" {
		return nil, errors.New("DATABASE_URL is not set (make verify / make step export it)")
	}
	srv, err := pgxpool.New(ctx, server)
	if err != nil {
		return nil, err
	}
	defer srv.Close()
	name := fmt.Sprintf("admission_test_%d", time.Now().UnixNano())
	if _, err := srv.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return nil, fmt.Errorf("create test database: %w", err)
	}
	dbMu.Lock()
	created = append(created, name)
	dbMu.Unlock()
	u, err := url.Parse(server)
	if err != nil {
		return nil, err
	}
	u.Path = "/" + name
	db := &testDB{name: name, wTrace: &sqlTracer{}, rTrace: &sqlTracer{}}
	if db.admin, err = pgxpool.New(ctx, u.String()); err != nil {
		return nil, err
	}
	if migrate {
		if err := applyAll(ctx, db.admin, "up", false); err != nil {
			return nil, err
		}
	}
	open := func(tr *sqlTracer) (*pgxpool.Pool, error) {
		cfg, err := pgxpool.ParseConfig(u.String())
		if err != nil {
			return nil, err
		}
		cfg.ConnConfig.Tracer = tr
		return pgxpool.NewWithConfig(ctx, cfg)
	}
	if db.writer, err = open(db.wTrace); err != nil {
		return nil, err
	}
	db.reader, err = open(db.rTrace)
	return db, err
}

func dropDatabases() {
	server := specsuite.DatabaseURL()
	if server == "" {
		return
	}
	ctx := context.Background()
	srv, err := pgxpool.New(ctx, server)
	if err != nil {
		return
	}
	defer srv.Close()
	for _, n := range created {
		_, _ = srv.Exec(ctx, "DROP DATABASE IF EXISTS "+n+" WITH (FORCE)")
	}
}

func sharedDB(ctx context.Context) (*testDB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if shared != nil {
		return shared, nil
	}
	dbMu.Unlock()
	db, err := newDatabase(ctx, true)
	dbMu.Lock()
	if err == nil {
		shared = db
	}
	return db, err
}

// ---------------------------------------------------------------- world

type storeWorld struct {
	ctx   context.Context
	db    *testDB
	store *postgres.Bookings

	results []domain.Booking
	errs    []error
	count   domain.Admission
	plan    string
	seq     int

	migrateErr error
}

func initScenario(sc *godog.ScenarioContext) {
	w := &storeWorld{ctx: context.Background()}

	registerTickSteps(sc)

	sc.Step(`^a Postgres database with all migrations applied$`, w.sharedDatabase)
	sc.Step(`^event "([^"]*)" exists$`, w.eventExists)
	sc.Step(`^an empty database$`, w.emptyDatabase)

	sc.Step(`^the store inserts a GRANTED booking for "([^"]*)" with queue_token "([^"]*)", booking_id "([^"]*)", grant_token "([^"]*)"$`, w.storeInserts)
	sc.Step(`^the store inserts a GRANTED booking for "([^"]*)" with queue_token "([^"]*)"$`, func(ev, qt string) error {
		w.seq++
		return w.storeInserts(ev, qt, fmt.Sprintf("auto-b%d", w.seq), fmt.Sprintf("auto-g%d", w.seq))
	})
	sc.Step(`^a booking with grant_token "([^"]*)" exists$`, func(gt string) error {
		return w.seedBooking("E1", "GRANTED", "seed-q", gt)
	})
	sc.Step(`^the store inserts a different booking with grant_token "([^"]*)"$`, func(gt string) error {
		return w.storeInserts("E1", "other-q", "other-b", gt)
	})
	sc.Step(`^bookings has exactly (\d+) rows? for "([^"]*)" and queue_token "([^"]*)"$`, w.rowsFor)
	sc.Step(`^both calls return booking_id "([^"]*)" and grant_token "([^"]*)"$`, w.bothReturn)
	sc.Step(`^both inserts succeed$`, w.noErrors)
	sc.Step(`^the insert fails with a unique violation on grant_token$`, w.grantTokenViolation)

	sc.Step(`^"([^"]*)" has one booking with status "([^"]*)"$`, func(ev, status string) error {
		return w.seedBooking(ev, status, "q-"+status, "g-"+status)
	})
	sc.Step(`^"E1" has (\d+) GRANTED bookings and "E2" has (\d+) GRANTED bookings$`, func(a, b int) error {
		for i := 0; i < a; i++ {
			if err := w.seedBooking("E1", "GRANTED", fmt.Sprintf("e1-q%d", i), fmt.Sprintf("e1-g%d", i)); err != nil {
				return err
			}
		}
		for i := 0; i < b; i++ {
			if err := w.seedBooking("E2", "GRANTED", fmt.Sprintf("e2-q%d", i), fmt.Sprintf("e2-g%d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the store counts admission for "([^"]*)"$`, func(ev string) error {
		w.db.wTrace.reset()
		w.db.rTrace.reset()
		var err error
		w.count, err = w.store.CountAdmission(w.ctx, ev)
		return err
	})
	sc.Step(`^open is (\d+) and browsing is (\d+)$`, func(open, browsing int64) error {
		if w.count.Open != open || w.count.Browsing != browsing {
			return fmt.Errorf("open/browsing = %d/%d, want %d/%d", w.count.Open, w.count.Browsing, open, browsing)
		}
		return nil
	})

	sc.Step(`^"E1" and (\d+) other events have (\d+) bookings, of which (\d+) are active for "E1"$`, w.bigTable)
	sc.Step(`^I EXPLAIN the admission count for "([^"]*)"$`, w.explain)
	sc.Step(`^the plan uses the partial index on \(event_id, status\)$`, func() error {
		if strings.Count(w.plan, "bookings_event_active_idx") < 2 { // custom and generic plan
			return fmt.Errorf("plan does not use bookings_event_active_idx:\n%s", w.plan)
		}
		return nil
	})
	sc.Step(`^the plan does not contain a sequential scan on bookings$`, func() error {
		if strings.Contains(w.plan, "Seq Scan on bookings") {
			return fmt.Errorf("plan has a sequential scan:\n%s", w.plan)
		}
		return nil
	})
	sc.Step(`^the query is sent through the writer connection pool, not the reader pool$`, func() error {
		if !w.db.wTrace.saw("bookings") {
			return errors.New("the count did not go through the writer pool")
		}
		if n := w.db.rTrace.count(); n != 0 {
			return fmt.Errorf("the reader pool saw %d statements", n)
		}
		return nil
	})

	sc.Step(`^all migrations are applied, rolled back and applied again$`, func() error {
		for _, step := range []struct {
			suffix  string
			reverse bool
		}{{"up", false}, {"down", true}, {"up", false}} {
			if w.migrateErr = applyAll(w.ctx, w.db.admin, step.suffix, step.reverse); w.migrateErr != nil {
				return nil
			}
		}
		return nil
	})
	sc.Step(`^no step fails$`, func() error { return w.migrateErr })
}

func (w *storeWorld) sharedDatabase() error {
	db, err := sharedDB(w.ctx)
	if err != nil {
		return err
	}
	w.db = db
	if _, err := db.admin.Exec(w.ctx, "TRUNCATE bookings CASCADE"); err != nil {
		return err
	}
	w.store = postgres.NewBookings(db.writer)
	return nil
}

func (w *storeWorld) emptyDatabase() error {
	db, err := newDatabase(w.ctx, false)
	if err != nil {
		return err
	}
	w.db = db
	return nil
}

func (w *storeWorld) eventExists(id string) error {
	_, err := w.db.admin.Exec(w.ctx,
		`INSERT INTO events (id, name, event_date) VALUES ($1, $1, '2026-12-01') ON CONFLICT DO NOTHING`, id)
	return err
}

// seedBooking inserts a row directly, with the extra columns a non-GRANTED status needs.
func (w *storeWorld) seedBooking(event, status, queueToken, grantToken string) error {
	if err := w.eventExists(event); err != nil {
		return err
	}
	_, err := w.db.admin.Exec(w.ctx, `
INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at)
VALUES ($1, $2, $3, $4, $5, 'a@example.com', now() + interval '5 minutes')`,
		"seed-"+queueToken+"-"+event, event, queueToken, status, grantToken)
	return err
}

func (w *storeWorld) storeInserts(ev, qt, bookingID, grantToken string) error {
	now := time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)
	b, err := w.store.InsertGranted(w.ctx, domain.Grant{
		EventID: ev, QueueToken: qt, BookingID: bookingID, GrantToken: grantToken,
		GrantedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	})
	w.results = append(w.results, b)
	w.errs = append(w.errs, err)
	return nil
}

func (w *storeWorld) rowsFor(n int, ev, qt string) error {
	var got int
	err := w.db.admin.QueryRow(w.ctx, `SELECT count(*) FROM bookings WHERE event_id=$1 AND queue_token=$2`, ev, qt).Scan(&got)
	if err != nil {
		return err
	}
	if got != n {
		return fmt.Errorf("%d rows, want %d", got, n)
	}
	return nil
}

func (w *storeWorld) noErrors() error {
	for i, err := range w.errs {
		if err != nil {
			return fmt.Errorf("insert %d failed: %w", i+1, err)
		}
	}
	return nil
}

func (w *storeWorld) bothReturn(id, grantToken string) error {
	if err := w.noErrors(); err != nil {
		return err
	}
	for i, b := range w.results {
		if b.ID != id || b.GrantToken != grantToken {
			return fmt.Errorf("call %d returned %+v, want id %s grant_token %s", i+1, b, id, grantToken)
		}
	}
	return nil
}

func (w *storeWorld) grantTokenViolation() error {
	last := w.errs[len(w.errs)-1]
	var pg *pgconn.PgError
	if !errors.As(last, &pg) || pg.Code != "23505" || pg.ConstraintName != "bookings_grant_token_key" {
		return fmt.Errorf("error = %v, want unique violation on bookings_grant_token_key", last)
	}
	return nil
}

func (w *storeWorld) bigTable(others, total, active int) error {
	if err := w.eventExists("E1"); err != nil {
		return err
	}
	_, err := w.db.admin.Exec(w.ctx, `
INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at)
SELECT 'big-' || i,
       CASE WHEN i <= $2 THEN 'E1' ELSE 'X' || (i % $1) END,
       'big-q' || i,
       CASE WHEN i <= $2 OR i % 50 = 0 THEN 'GRANTED' ELSE 'CONFIRMED' END,
       'big-g' || i, 'a@example.com', now() + interval '5 minutes'
FROM generate_series(1, $3) AS i`, others, active, total)
	if err != nil {
		return err
	}
	_, err = w.db.admin.Exec(w.ctx, "ANALYZE bookings")
	return err
}

func (w *storeWorld) explain(ev string) error {
	// Both plans must use the index: the custom plan of a first call, and the generic plan
	// pgx's statement cache switches to after a few executions in production.
	conn, err := w.db.admin.Acquire(w.ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var sb strings.Builder
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		if _, err := conn.Exec(w.ctx, "SET plan_cache_mode = "+mode); err != nil {
			return err
		}
		rows, err := conn.Query(w.ctx, "EXPLAIN "+postgres.CountSQL, ev)
		if err != nil {
			return err
		}
		sb.WriteString("-- " + mode + "\n")
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				return err
			}
			sb.WriteString(line + "\n")
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	w.plan = sb.String()
	return nil
}
