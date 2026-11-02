package mapper

import (
	"fmt"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/valueobject"
)

// ToSavedNIKItem converts a SavedNIK entity and its raw NIK (at the time of
// verification, carried by the caller) to a DTO safe for the wire. The raw NIK
// is masked before being included in the response.
func ToSavedNIKItem(n entity.SavedNIK, rawNIK string) dto.SavedNIKItem {
	return dto.SavedNIKItem{
		ID:         n.ID,
		NIKMasked:  valueobject.MaskNIK(rawNIK),
		Label:      n.Label,
		VerifiedAt: n.VerifiedAt.UTC().Format(time.RFC3339),
		Status:     "VERIFIED",
	}
}

// ToMyTicketItem converts a UserTicket entity to a DTO.
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

// ToETicketResponse converts a UserTicket entity to an ETicketResponse. The
// nikMasked argument is the display form of the holder's NIK; the use case is
// responsible for computing it from the stored HMAC before calling this mapper.
func ToETicketResponse(t entity.UserTicket, nikMasked string) dto.ETicketResponse {
	return dto.ETicketResponse{
		TicketID: t.TicketID,
		Event: dto.ETicketEvent{
			Name:  t.EventName,
			Date:  t.EventDate,
			Time:  t.EventTime,
			Venue: t.EventVenue,
		},
		Category: t.Category,
		Holder:   dto.ETicketHolder{NIKMasked: nikMasked},
		QRCode:   t.QRPayload, // In production: base64-encoded PNG; POC returns the raw value
		QRValue:  t.QRPayload,
		Instructions: "Tunjukkan QR code dan identitas pemegang saat masuk venue.",
	}
}
