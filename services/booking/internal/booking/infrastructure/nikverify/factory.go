package nikverify

import (
	"context"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
)

// New picks the adapter: the Lambda invocation when NIK_VERIFY_FUNCTION_NAME is set, the local fake only when it is
// not and APP_ENV is local. Outside local an empty name is refused by config.Load, so the fake cannot run deployed.
func New(ctx context.Context, cfg config.Config) (outbound.NIKVerifier, error) {
	if cfg.NIKVerify.FunctionName == "" && cfg.App.Env == "local" {
		return NewFake(), nil
	}
	return Connect(ctx, cfg.NIKVerify.FunctionName, cfg.NIKVerify.Timeout)
}
