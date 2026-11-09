package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
)

var ctx = context.Background()

// migration returns the text of migrations/<name>, found by walking up from this folder.
func migration(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "migrations", name)); err == nil {
			return string(b)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("migrations/%s not found", name)
		}
		dir = parent
	}
}

func exists(t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND ($2 = '' OR column_name = $2)`,
		table, column).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// seed writes an event, one category and one SELECTING booking, the minimum the booking tables refer to.
func seed(t *testing.T, pool *pgxpool.Pool, booking string) {
	t.Helper()
	stmts := []string{
		`INSERT INTO events (id, name, event_date) VALUES ('evt_a', 'Konser A', '2026-12-25') ON CONFLICT DO NOTHING`,
		`INSERT INTO ticket_categories (id, event_id, name, price, capacity, available, sort_order)
		   VALUES ('cat_vip', 'evt_a', 'VIP', 1500000, 100, 100, 1) ON CONFLICT DO NOTHING`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, booking_expires_at)
		VALUES ($1, 'evt_a', 'qt_'||$1, 'SELECTING', 'gt_'||$1, now() + interval '5 minutes')`, booking)
	if err != nil {
		t.Fatal(err)
	}
}

func item(pool *pgxpool.Pool, id, booking, nikHmac string) error {
	_, err := pool.Exec(ctx, `INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked, holder_email)
		VALUES ($1, $2, 'evt_a', 'cat_vip', 'VIP', 1500000, $3, '3171********0001', 'a@example.com')`, id, booking, nikHmac)
	return err
}

func TestTheBookingMigrationsApplyAndTheirDownFilesReverseThem(t *testing.T) {
	pool := pgtest.New(t)
	for _, tc := range []struct{ table, column string }{
		{"ticket_categories", "sort_order"},
		{"booking_items", "nik_masked"},
		{"nik_claims", ""},
		{"tickets", "booking_item_id"},
	} {
		if !exists(t, pool, tc.table, tc.column) {
			t.Fatalf("after the up migrations %s.%s is missing", tc.table, tc.column)
		}
	}
	for _, f := range []string{"0008_booking_domain.down.sql", "0007_ticket_categories.down.sql"} {
		if _, err := pool.Exec(ctx, migration(t, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for _, tc := range []struct{ table, column string }{
		{"ticket_categories", ""}, {"booking_items", ""}, {"nik_claims", ""}, {"tickets", "booking_item_id"},
	} {
		if exists(t, pool, tc.table, tc.column) {
			t.Fatalf("after the down migrations %s %s still exists", tc.table, tc.column)
		}
	}
	for _, f := range []string{"0007_ticket_categories.up.sql", "0008_booking_domain.up.sql"} {
		if _, err := pool.Exec(ctx, migration(t, f)); err != nil {
			t.Fatalf("re-applying %s after its down: %v", f, err)
		}
	}
}

func TestASecondItemWithTheSameNIKInOneBookingIsRefused(t *testing.T) {
	pool := pgtest.New(t)
	seed(t, pool, "RB-1")
	if err := item(pool, "it_1", "RB-1", "hmac_nik_1"); err != nil {
		t.Fatal(err)
	}
	if err := item(pool, "it_2", "RB-1", "hmac_nik_1"); !uniqueViolation(err) {
		t.Fatalf("second seat with the same NIK in one booking: err = %v, want a unique violation", err)
	}
}

func TestASecondClaimOfANIKInOneEventIsRefused(t *testing.T) {
	pool := pgtest.New(t)
	seed(t, pool, "RB-1")
	seed(t, pool, "RB-2")
	claim := `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ('evt_a', 'hmac_nik_1', $1)`
	if _, err := pool.Exec(ctx, claim, "RB-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, claim, "RB-2"); !uniqueViolation(err) {
		t.Fatalf("same NIK claimed by a second booking of the event: err = %v, want a unique violation (BR-02)", err)
	}
}

func TestASecondTicketForTheSameBookingItemIsRefused(t *testing.T) {
	pool := pgtest.New(t)
	seed(t, pool, "RB-1")
	if err := item(pool, "it_1", "RB-1", "hmac_nik_1"); err != nil {
		t.Fatal(err)
	}
	ticket := `INSERT INTO tickets (id, booking_id, event_id, nik_hmac, nik_masked, holder_email, category_id, category_name, price, qr_nonce, booking_item_id)
		VALUES ($1, 'RB-1', 'evt_a', 'hmac_nik_1', '3171********0001', 'a@example.com', 'cat_vip', 'VIP', 1500000, $1, 'it_1')`
	if _, err := pool.Exec(ctx, ticket, "TKT-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ticket, "TKT-2"); !uniqueViolation(err) {
		t.Fatalf("second ticket for one seat: err = %v, want a unique violation (no duplicate QR on outbox retry)", err)
	}
}

// Email and terms are entered at POST /bookings (HELD). A booking that never got there (claimed, then expired or
// abandoned) has neither, and must still be storable; from HELD on both are required.
func TestEmailAndTermsAreRequiredOnlyFromHeldOn(t *testing.T) {
	pool := pgtest.New(t)
	seed(t, pool, "RB-1") // SELECTING, no email, no terms
	for _, s := range []string{"EXPIRED", "ABANDONED"} {
		if _, err := pool.Exec(ctx, `UPDATE bookings SET status = $1 WHERE id = 'RB-1'`, s); err != nil {
			t.Fatalf("a booking without email or terms must be able to become %s: %v", s, err)
		}
	}
	for _, s := range []string{"HELD", "AWAITING_PAYMENT", "PENDING_CANCELLATION", "CONFIRMED"} {
		_, err := pool.Exec(ctx, `UPDATE bookings SET status = $1 WHERE id = 'RB-1'`, s)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "23514" {
			t.Fatalf("%s without email or terms: err = %v, want a check violation", s, err)
		}
	}
}

// The SELECTING sweep (build step B2) runs every second on the writer. Both plans must use the partial index:
// the custom plan of a first call and the generic plan pgx switches to after a few executions.
func TestTheSelectingDeadlineQueryUsesItsPartialIndex(t *testing.T) {
	pool := pgtest.New(t)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	const q = `SELECT id FROM bookings WHERE status = 'SELECTING' AND booking_expires_at <= $1`
	var plan strings.Builder
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		if _, err := conn.Exec(ctx, "SET plan_cache_mode = "+mode); err != nil {
			t.Fatal(err)
		}
		rows, err := conn.Query(ctx, "EXPLAIN "+q, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line)
			plan.WriteString("\n")
		}
		rows.Close()
	}
	if strings.Count(plan.String(), "bookings_selecting_deadline_idx") < 2 {
		t.Fatalf("both plans must use bookings_selecting_deadline_idx:\n%s", plan.String())
	}
}
