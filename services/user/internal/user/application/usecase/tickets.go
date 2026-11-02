package usecase

import (
	"context"
	"math"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
)

const defaultLimit = 10
const maxLimit = 50

// GetMyTickets returns a paginated list of tickets belonging to the account.
// Implements GET /users/me/tickets (Section 6.1).
func (u *UserUsecase) GetMyTickets(ctx context.Context, accountID string, req dto.GetMyTicketsRequest) (dto.MyTicketsResponse, error) {
	if req.Limit <= 0 {
		req.Limit = defaultLimit
	}
	if req.Limit > maxLimit {
		req.Limit = maxLimit
	}
	if req.Offset < 0 {
		req.Offset = 0
	}

	tickets, total, err := u.repo.FindTicketsByAccountID(ctx, accountID, req)
	if err != nil {
		return dto.MyTicketsResponse{}, apperror.ErrInternal("failed to retrieve tickets")
	}

	items := make([]dto.MyTicketItem, 0, len(tickets))
	for _, t := range tickets {
		items = append(items, mapper.ToMyTicketItem(t))
	}

	totalPage := int(math.Ceil(float64(total) / float64(req.Limit)))
	if totalPage == 0 {
		totalPage = 1
	}

	return dto.MyTicketsResponse{
		Pagination: dto.PaginationMeta{
			TotalData: total,
			TotalPage: totalPage,
			Limit:     req.Limit,
			Offset:    req.Offset,
		},
		Tickets: items,
	}, nil
}

// GetETicket returns the full e-ticket data for display/download.
// Implements GET /tickets/{ticket_id}/eticket (FR-27, Section 6.2).
func (u *UserUsecase) GetETicket(ctx context.Context, accountID, ticketID string) (dto.ETicketResponse, error) {
	ticket, err := u.repo.FindTicketByID(ctx, accountID, ticketID)
	if err != nil {
		// Not found or belongs to a different account — identical response to
		// avoid confirming ticket existence to unauthorised callers.
		return dto.ETicketResponse{}, apperror.ErrNotFound("ticket not found")
	}

	// The NIK HMAC is stored on the ticket. In production the masked form would
	// be stored separately or derived from a reversible scheme. For the POC we
	// return a generic masked placeholder.
	nikMasked := maskStoredNIK(ticket.NIKHmac)

	return mapper.ToETicketResponse(ticket, nikMasked), nil
}
