package usecase

import (
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/domain/template"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/port/inbound"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification/internal/notification/port/outbound"
)

// NotifierUsecase renders queued events and sends them.
type NotifierUsecase struct {
	renderer *template.Renderer
	sender   portout.IEmailSender
	dedupe   portout.IDedupeStore
}

// NewNotifier wires the use case and returns the inbound port.
func NewNotifier(
	renderer *template.Renderer,
	sender portout.IEmailSender,
	dedupe portout.IDedupeStore,
) portin.INotifierUsecase {
	return &NotifierUsecase{renderer: renderer, sender: sender, dedupe: dedupe}
}
