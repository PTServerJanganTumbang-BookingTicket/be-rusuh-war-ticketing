package mapper

import (
	"fmt"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

// ToSavedNIKItem converts a SavedNIK to its wire form.
//
// The mask is read from the stored column, not derived from a raw NIK the
// caller happens to be holding. That is what makes the create and list
// responses agree — deriving it at create time only was the bug that made
// listing return placeholders.
func ToSavedNIKItem(n entity.SavedNIK) dto.SavedNIKItem {
	return dto.SavedNIKItem{
		ID:         n.ID,
		NIKMasked:  n.NIKMasked,
		Label:      n.Label,
		VerifiedAt: n.VerifiedAt.UTC().Format(time.RFC3339),
		Status:     "VERIFIED",
	}
}

// ToUserSummary converts an Account to the summary embedded in auth responses.
func ToUserSummary(a entity.Account) dto.UserSummary {
	return dto.UserSummary{
		ID:    a.ID,
		Email: a.Email,
		NIK:   a.NIKMasked,
	}
}

// ToAccountProfile converts an Account to the profile payload of
// GET /accounts/me (contract §1.8).
//
// Two fields out of an Account that holds eight. Everything else the row knows
// is either a credential or evidence: NIKHmac is what the gate matches against,
// DukcapilRef is audit material, and the timestamps answer "may this account
// purchase" (BR-11) rather than anything a profile page shows. A profile
// endpoint that returned the whole entity would be leaking by default and
// relying on nobody noticing.
//
// NIKMasked passes through as stored, never re-derived: the mask is the only
// form of the NIK this service keeps beside the hash, and a hash cannot be
// unmasked. An account mid-registration has "" here, which the page reads as
// "not yet verified".
func ToAccountProfile(a entity.Account) dto.AccountProfileResponse {
	return dto.AccountProfileResponse{
		Email:     a.Email,
		NIKMasked: a.NIKMasked,
	}
}

// ToRedirectContext converts an Engagement to the wire form (BR-09).
//
// The message is a human-readable hint, not a label key: contract §1.3 shows
// prose here, and the frontend routes on type, not on this string.
func ToRedirectContext(e entity.Engagement) *dto.RedirectContext {
	if !e.IsActive() {
		return nil
	}
	ctx := &dto.RedirectContext{
		Type:    string(e.Kind),
		EventID: e.EventID,
	}
	switch e.Kind {
	case entity.EngagementBooking:
		ctx.BookingID = e.BookingID
		ctx.Message = "Booking context restored"
	case entity.EngagementQueue:
		ctx.Message = "Queue position restored"
	}
	return ctx
}

// ToMyTicketItem converts a UserTicket to a list row.
func ToMyTicketItem(t entity.UserTicket) dto.MyTicketItem {
	return dto.MyTicketItem{
		TicketID:  t.TicketID,
		BookingID: t.BookingID,
		Event: dto.EventSummary{
			ID:       t.EventID,
			Name:     t.EventName,
			Date:     t.EventDate,
			Venue:    t.EventVenue,
			ImageURL: t.EventImageURL,
		},
		Category:   t.Category,
		Price:      t.Price,
		Status:     string(t.Status),
		ETicketURL: fmt.Sprintf("/tickets/%s/eticket", t.TicketID),
	}
}

// ToETicketResponse converts a UserTicket to the e-ticket payload.
//
// qrValue is the opaque signed value, not the holder's identity: a QR is
// screenshotted, printed and photographed, so it carries nothing that would be
// damaging in the open. The gate resolves the ticket from it and matches the
// attendee's KTP against the stored hash.
func ToETicketResponse(t entity.UserTicket, qrValue string) dto.ETicketResponse {
	return dto.ETicketResponse{
		TicketID: t.TicketID,
		Event: dto.ETicketEvent{
			Name:  t.EventName,
			Date:  t.EventDate,
			Time:  t.EventTime,
			Venue: t.EventVenue,
		},
		Category:     t.Category,
		Holder:       dto.ETicketHolder{NIKMasked: t.NIKMasked},
		QRCode:       qrValue,
		QRValue:      qrValue,
		Instructions: "Tunjukkan QR code dan identitas pemegang saat masuk venue.",
	}
}
