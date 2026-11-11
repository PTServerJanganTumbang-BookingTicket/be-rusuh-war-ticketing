package usecase

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// Handoff credentials (ADR-007): a prefix and 256 random bits, base64url without padding (43 characters).
const (
	HandoffCodePrefix    = "hc_"
	PaymentSessionPrefix = "ps_"
	// HandoffCodeTTL is how long the app has to open the WebView and the page has to exchange the code.
	HandoffCodeTTL = 60 * time.Second
	// maxHandoffCodeLen bounds what the exchange accepts before touching Redis (real codes are 46).
	maxHandoffCodeLen = 64
	maxBookingIDLen   = 64
)

// HandoffUsecase implements §5.6 and §5.7 (docs/07-payment/07, 08).
type HandoffUsecase struct {
	bookings portout.IBookingRepository
	store    portout.IHandoffStore
	limiter  portout.IHandoffLimiter

	now    func() time.Time
	random func() (string, error)
}

// NewHandoff wires the handoff use case.
func NewHandoff(bookings portout.IBookingRepository, store portout.IHandoffStore, limiter portout.IHandoffLimiter) portin.IHandoffUsecase {
	return &HandoffUsecase{
		bookings: bookings,
		store:    store,
		limiter:  limiter,
		now:      func() time.Time { return time.Now().UTC() },
		random:   random256,
	}
}

func random256() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateHandoff is §5.6. The grant guard has already checked the grant, the device (guest) and the session (account).
func (u *HandoffUsecase) CreateHandoff(ctx context.Context, bookingID, grantToken string) (dto.HandoffResponse, error) {
	allowed, retryAfter, err := u.limiter.Allow(ctx, grantToken)
	switch {
	case err != nil:
		// Fails open, like join: the limiter guards against a client loop, the code is the security control.
		slog.WarnContext(ctx, "handoff rate limiter unavailable, allowing", slog.Any("err", err))
	case !allowed:
		seconds := int(math.Ceil(retryAfter.Seconds()))
		return dto.HandoffResponse{}, apperror.ErrTooManyRequests("Terlalu banyak permintaan").
			WithCode(apperror.CodeTooManyRequests).
			WithRetryAfter(retryAfter).
			WithData(map[string]any{"retry_after": max(seconds, 1)})
	}

	b, err := u.bookings.GetOwned(ctx, bookingID, grantToken)
	if err != nil {
		return dto.HandoffResponse{}, mapBookingErr(err)
	}
	now := u.now()
	if err := payable(b, now); err != nil {
		return dto.HandoffResponse{}, err
	}

	secret, err := u.random()
	if err != nil {
		return dto.HandoffResponse{}, unavailable(ctx, "handoff code entropy", err)
	}
	code := HandoffCodePrefix + secret
	created, err := u.store.PutCode(ctx, code, grantToken, HandoffCodeTTL)
	if err != nil || !created {
		// !created is a collision on 256 random bits: treat it as the failure it would have to be.
		return dto.HandoffResponse{}, unavailable(ctx, "handoff code write", err)
	}
	return dto.HandoffResponse{
		BookingID:   b.ID,
		HandoffCode: code,
		ExpiresAt:   now.Add(HandoffCodeTTL).Format(time.RFC3339),
	}, nil
}

// payable decides whether a handoff may be issued (docs/07-payment/07 §3 step 4). data.status uses the four values of
// GET /bookings/{id} (§4.4), so the app routes the same way it does there.
func payable(b entity.Booking, now time.Time) error {
	invalid := func(status string) error {
		return apperror.ErrConflict("Booking tidak dalam status yang dapat dibayar").
			WithCode(apperror.CodeBookingStatusInvalid).
			WithData(map[string]any{"status": status})
	}
	switch b.Status {
	case entity.StatusGranted, entity.StatusSelecting:
		return invalid("SELECTING")
	case entity.StatusConfirmed:
		return invalid("PAID")
	case entity.StatusExpired, entity.StatusLapsed, entity.StatusAbandoned:
		return invalid("EXPIRED")
	case entity.StatusHeld, entity.StatusAwaitingPayment:
		if b.PaymentExpiresAt != nil && !now.Before(*b.PaymentExpiresAt) {
			return apperror.ErrConflict("Waktu pembayaran sudah habis").
				WithCode(apperror.CodePaymentWindowClosed).
				WithData(map[string]any{"payment_expires_at": b.PaymentExpiresAt.UTC().Format(time.RFC3339)})
		}
		return nil
	case entity.StatusPendingCancellation:
		// Past the deadline but inside the 30 s grace: a payment may still land (FR-23a, FR-23b).
		return nil
	default:
		// Every status in the bookings CHECK constraint is handled above. A new one must be mapped to one of the four
		// §4.4 values here, never sent raw.
		slog.Error("handoff: unmapped booking status", slog.String("status", string(b.Status)))
		return apperror.ErrInternal("unmapped booking status")
	}
}

// ExchangeHandoff is §5.7. The code is spent on the first attempt, whatever happens after the GETDEL.
func (u *HandoffUsecase) ExchangeHandoff(ctx context.Context, req dto.ExchangeRequest) (dto.ExchangeResponse, error) {
	if req.BookingID == "" || len(req.BookingID) > maxBookingIDLen ||
		!strings.HasPrefix(req.HandoffCode, HandoffCodePrefix) || len(req.HandoffCode) > maxHandoffCodeLen {
		return dto.ExchangeResponse{}, apperror.ErrBadRequest("booking_id dan handoff_code wajib").
			WithCode(apperror.CodeValidationError)
	}
	codeInvalid := apperror.ErrUnauthorized("Kode pembayaran tidak berlaku").WithCode(apperror.CodeHandoffCodeInvalid)
	grantGone := apperror.ErrUnauthorized("Sesi booking sudah berakhir").WithCode(grantauth.CodeGrantTokenInvalid)

	grantToken, found, err := u.store.TakeCode(ctx, req.HandoffCode)
	if err != nil {
		return dto.ExchangeResponse{}, unavailable(ctx, "handoff code read", err)
	}
	if !found {
		return dto.ExchangeResponse{}, codeInvalid
	}
	bookingID, ttl, found, err := u.store.GrantBooking(ctx, grantToken)
	if err != nil {
		return dto.ExchangeResponse{}, unavailable(ctx, "grant read", err)
	}
	if !found {
		return dto.ExchangeResponse{}, grantGone
	}
	if subtle.ConstantTimeCompare([]byte(bookingID), []byte(req.BookingID)) != 1 {
		return dto.ExchangeResponse{}, codeInvalid
	}
	if ttl <= 0 {
		// admission-ctl writes the grant and its TTL in one MULTI; a grant without one is a bug. Never mint a session
		// that would not die.
		slog.ErrorContext(ctx, "handoff: grant without a TTL", slog.String("booking_id", bookingID))
		return dto.ExchangeResponse{}, grantGone
	}

	secret, err := u.random()
	if err != nil {
		return dto.ExchangeResponse{}, unavailable(ctx, "payment session entropy", err)
	}
	session := PaymentSessionPrefix + secret
	created, err := u.store.PutSession(ctx, session, grantToken, ttl)
	if err != nil || !created {
		return dto.ExchangeResponse{}, unavailable(ctx, "payment session write", err)
	}
	return dto.ExchangeResponse{
		BookingID:      bookingID,
		PaymentSession: session,
		ExpiresAt:      u.now().Add(ttl).Format(time.RFC3339),
	}, nil
}

func unavailable(ctx context.Context, what string, err error) error {
	slog.WarnContext(ctx, "handoff: "+what+" failed", slog.Any("err", err))
	return apperror.ErrServiceUnavailable("Layanan sedang sibuk, coba lagi").WithCode(apinizer.ErrorCodeInternal)
}
