package mapper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/entity"
)

func TestToSavedNIKItem_UsesTheStoredMask(t *testing.T) {
	verifiedAt := time.Date(2026, 10, 20, 8, 11, 0, 0, time.UTC)
	item := ToSavedNIKItem(entity.SavedNIK{
		ID: "SNK-1", NIKHmac: "hash", NIKMasked: "3171**********33",
		Label: "Budi", VerifiedAt: verifiedAt,
	})
	assert.Equal(t, "SNK-1", item.ID)
	assert.Equal(t, "3171**********33", item.NIKMasked)
	assert.Equal(t, "2026-10-20T08:11:00Z", item.VerifiedAt)
	assert.Equal(t, "VERIFIED", item.Status)
}

func TestToAccountProfile_CarriesOnlyEmailAndTheMask(t *testing.T) {
	now := time.Date(2026, 11, 3, 9, 12, 0, 0, time.UTC)
	p := ToAccountProfile(entity.Account{
		ID: "USR-001", Email: "alex@example.com",
		EmailVerifiedAt: &now,
		NIKHmac:         "deadbeef", NIKMasked: "3171********xx22",
		NIKVerifiedAt: &now, DukcapilRef: "DKP-99", CreatedAt: now,
	})
	// Compared whole, not field by field: that is what pins the response to two
	// fields. The hash is what the gate matches against and the ref is audit
	// material, neither has any business on a profile page, and the account
	// stores no name to return at all (FR-15a).
	assert.Equal(t, dto.AccountProfileResponse{
		Email:     "alex@example.com",
		NIKMasked: "3171********xx22",
	}, p)
}

// An account created by a magic-link sign-in has no NIK yet. The mapper reports
// that as an empty mask rather than inventing a placeholder.
func TestToAccountProfile_UnregisteredAccountHasAnEmptyMask(t *testing.T) {
	p := ToAccountProfile(entity.Account{ID: "USR-002", Email: "new@example.com"})
	assert.Equal(t, "new@example.com", p.Email)
	assert.Empty(t, p.NIKMasked)
}

func TestToUserSummary_CarriesTheMaskNotTheHash(t *testing.T) {
	s := ToUserSummary(entity.Account{
		ID: "USR-001", Email: "a@example.com",
		NIKHmac: "deadbeef", NIKMasked: "3171**********22",
	})
	assert.Equal(t, "3171**********22", s.NIK)
	assert.NotContains(t, s.NIK, "deadbeef")
}

func TestToUserSummary_OmitsNIKWhenUnset(t *testing.T) {
	s := ToUserSummary(entity.Account{ID: "USR-001", Email: "a@example.com"})
	assert.Empty(t, s.NIK)
}

func TestToRedirectContext_Booking(t *testing.T) {
	ctx := ToRedirectContext(entity.Engagement{
		Kind: entity.EngagementBooking, EventID: "evt_a", BookingID: "RB-1",
	})
	require.NotNil(t, ctx)
	assert.Equal(t, "booking", ctx.Type)
	assert.Equal(t, "evt_a", ctx.EventID)
	assert.Equal(t, "RB-1", ctx.BookingID)
	assert.NotEmpty(t, ctx.Message)
}

func TestToRedirectContext_Queue(t *testing.T) {
	ctx := ToRedirectContext(entity.Engagement{Kind: entity.EngagementQueue, EventID: "evt_a"})
	require.NotNil(t, ctx)
	assert.Equal(t, "queue", ctx.Type)
	assert.Empty(t, ctx.BookingID, "a queue position has no booking yet")
}

func TestToRedirectContext_NilWhenInactive(t *testing.T) {
	// Omitted rather than sent empty, so the frontend's check is "is it there".
	assert.Nil(t, ToRedirectContext(entity.Engagement{Kind: entity.EngagementNone}))
	assert.Nil(t, ToRedirectContext(entity.Engagement{}))
}

func TestToMyTicketItem(t *testing.T) {
	item := ToMyTicketItem(entity.UserTicket{
		TicketID: "TKT-001", BookingID: "RB-00912", EventID: "evt_a",
		EventName: "Konser A", EventDate: "2026-12-25", EventVenue: "GBK",
		Category: "VIP", Price: 1500000, Status: entity.TicketStatusValid,
	})
	assert.Equal(t, "/tickets/TKT-001/eticket", item.ETicketURL)
	assert.Equal(t, "VALID", item.Status)
	assert.Equal(t, "Konser A", item.Event.Name)
}

func TestToETicketResponse_HolderIsMaskedAndQRHasNoIdentity(t *testing.T) {
	resp := ToETicketResponse(entity.UserTicket{
		TicketID: "TKT-001", EventName: "Konser A", EventTime: "19:00",
		Category: "VIP", NIKMasked: "3171**********22",
	}, "TKT-001.nonce-abc")

	assert.Equal(t, "3171**********22", resp.Holder.NIKMasked)
	assert.Equal(t, "TKT-001.nonce-abc", resp.QRValue)
	assert.Equal(t, resp.QRValue, resp.QRCode)
	assert.NotContains(t, resp.QRValue, "3171")
}
