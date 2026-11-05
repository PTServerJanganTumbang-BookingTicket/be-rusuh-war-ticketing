package usecase

import (
	"context"
	"errors"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/mapper"
)

const (
	defaultLimit = 10
	maxLimit     = 50
)

// GetMyTickets returns a page of the account's tickets.
//
// Implements GET /users/me/tickets (contract §6.1).
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

	return dto.MyTicketsResponse{
		Total:   total,
		Limit:   req.Limit,
		Offset:  req.Offset,
		Tickets: items,
	}, nil
}

// GetETicket returns the e-ticket for display or download.
//
// Ownership is enforced in the query, which binds the ticket to the caller.
// A ticket that exists but belongs to someone else returns 404, not 403 —
// a 403 confirms the ID is real (§9 object ownership).
//
// Implements GET /tickets/{ticket_id}/eticket (FR-27, contract §6.2).
func (u *UserUsecase) GetETicket(ctx context.Context, accountID, ticketID string) (dto.ETicketResponse, error) {
	ticket, err := u.repo.FindTicketByID(ctx, accountID, ticketID)
	if err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) && appErr.HTTPStatus == 404 {
			return dto.ETicketResponse{}, appErr
		}
		return dto.ETicketResponse{}, apperror.ErrNotFound("ticket not found")
	}

	// The QR carries the ticket reference and its nonce — nothing identifying.
	// A QR is screenshotted, printed and photographed in queues; the gate
	// resolves the ticket from this and matches the attendee's physical KTP
	// against the stored hash, so it never needs the NIK.
	qrValue := ticket.TicketID + "." + ticket.QRNonce

	return mapper.ToETicketResponse(ticket, qrValue), nil
}
