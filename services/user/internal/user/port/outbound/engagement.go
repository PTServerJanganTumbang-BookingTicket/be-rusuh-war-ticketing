package outbound

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// IEngagementRepository finds what a returning customer was in the middle of.
//
// Read-only, and deliberately so: queue-service owns queue_entries and
// booking-service owns bookings. This service answers "where do I send them
// back to" (BR-09) and must never be able to change either.
type IEngagementRepository interface {
	// FindActiveEngagement returns the account's one live involvement, or an
	// inactive Engagement when there is none.
	//
	// Absence is not an error: most logins have nothing in flight, and failing
	// the login because a customer is not mid-purchase would be absurd.
	FindActiveEngagement(ctx context.Context, accountID string) (entity.Engagement, error)
}
