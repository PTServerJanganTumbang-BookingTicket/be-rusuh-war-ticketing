package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// ownershipClause binds a ticket to the caller.
//
// Two ways to own one: you bought it, or your own registered NIK holds it.
// Deliberately NOT through saved_niks — a stranger's purchase for a NIK you
// happen to have saved would otherwise appear in your list and its e-ticket
// would be readable by you. FR-32d still holds: removing a saved NIK changes
// nothing here, because this join never touches that table.
const ownershipClause = `
  (t.purchaser_account_id = $1
   OR t.nik_hmac = (SELECT nik_hmac FROM accounts WHERE id = $1 AND nik_hmac IS NOT NULL))
`

// statusClause maps the contract's upcoming/past filter onto the EVENT DATE,
// not the ticket status. A VALID ticket for a concert that already happened is
// in the past, which status alone cannot express.
const statusClause = `
  ($2 = '' OR $2 = 'all'
   OR ($2 = 'upcoming' AND e.event_date >= CURRENT_DATE AND t.status = 'VALID')
   OR ($2 = 'past'     AND (e.event_date < CURRENT_DATE OR t.status <> 'VALID')))
`

const ticketColumns = `
  t.id, t.booking_id, t.event_id,
  COALESCE(e.name, ''), COALESCE(to_char(e.event_date, 'YYYY-MM-DD'), ''),
  COALESCE(e.event_time, ''), COALESCE(e.venue, ''), COALESCE(e.image_url, ''),
  t.category_name, t.price, t.nik_masked, t.qr_nonce, t.issued_at, t.status
`

func scanTicket(row pgx.Row) (entity.UserTicket, error) {
	var t entity.UserTicket
	var status string
	err := row.Scan(&t.TicketID, &t.BookingID, &t.EventID, &t.EventName, &t.EventDate,
		&t.EventTime, &t.EventVenue, &t.EventImageURL, &t.Category, &t.Price,
		&t.NIKMasked, &t.QRNonce, &t.IssuedAt, &status)
	t.Status = entity.TicketStatus(status)
	return t, err
}

// FindTicketsByAccountID returns one page plus the total row count.
//
// The count is a second query rather than a window function so the page query
// stays index-friendly; both run against the same filter.
func (r *UserRepository) FindTicketsByAccountID(
	ctx context.Context, accountID string, req dto.GetMyTicketsRequest,
) ([]entity.UserTicket, int, error) {
	var total int
	countSQL := `SELECT count(*) FROM tickets t
	             LEFT JOIN events e ON e.id = t.event_id
	             WHERE ` + ownershipClause + ` AND ` + statusClause
	if err := r.pool.QueryRow(ctx, countSQL, accountID, req.Status).Scan(&total); err != nil {
		return nil, 0, apperror.ErrInternal("failed to count tickets")
	}

	pageSQL := `SELECT ` + ticketColumns + ` FROM tickets t
	            LEFT JOIN events e ON e.id = t.event_id
	            WHERE ` + ownershipClause + ` AND ` + statusClause + `
	            ORDER BY t.issued_at DESC, t.id
	            LIMIT $3 OFFSET $4`
	rows, err := r.pool.Query(ctx, pageSQL, accountID, req.Status, req.Limit, req.Offset)
	if err != nil {
		return nil, 0, apperror.ErrInternal("failed to read tickets")
	}
	defer rows.Close()

	tickets := make([]entity.UserTicket, 0, req.Limit)
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, 0, apperror.ErrInternal("failed to read tickets")
		}
		tickets = append(tickets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, apperror.ErrInternal("failed to read tickets")
	}
	return tickets, total, nil
}

// FindTicketByID returns one ticket the caller owns.
//
// Ownership is in the WHERE clause, so a ticket belonging to someone else is
// indistinguishable from one that does not exist — 404, never 403 (§9).
func (r *UserRepository) FindTicketByID(ctx context.Context, accountID, ticketID string) (entity.UserTicket, error) {
	sql := `SELECT ` + ticketColumns + ` FROM tickets t
	        LEFT JOIN events e ON e.id = t.event_id
	        WHERE t.id = $2 AND ` + ownershipClause
	t, err := scanTicket(r.pool.QueryRow(ctx, sql, accountID, ticketID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.UserTicket{}, apperror.ErrNotFound("ticket not found")
	}
	if err != nil {
		return entity.UserTicket{}, apperror.ErrInternal("failed to read ticket")
	}
	return t, nil
}
