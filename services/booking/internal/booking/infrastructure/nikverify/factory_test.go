package nikverify

import (
	"context"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
)

func TestLocallyWithoutAFunctionNameTheFakeRuns(t *testing.T) {
	v, err := New(context.Background(), config.Config{App: config.App{Env: "local"}})
	if _, ok := v.(*Fake); err != nil || !ok {
		t.Fatalf("verifier = %T (%v), want the local fake", v, err)
	}
}

func TestWithAFunctionNameTheLambdaIsInvokedEvenLocally(t *testing.T) {
	cfg := config.Config{App: config.App{Env: "local"}, NIKVerify: config.NIKVerify{FunctionName: "f", Timeout: time.Second}}
	v, err := New(context.Background(), cfg)
	if l, ok := v.(*Lambda); err != nil || !ok || l.name != "f" || l.timeout != time.Second {
		t.Fatalf("verifier = %#v (%v), want the Lambda adapter for f with a 1 s deadline", v, err)
	}
}

// Config.Load refuses this combination; the factory must not quietly fall back to the fake if it ever gets here.
func TestOutsideLocalAnEmptyFunctionNameNeverGivesTheFake(t *testing.T) {
	v, _ := New(context.Background(), config.Config{App: config.App{Env: "production"}})
	if _, isFake := v.(*Fake); isFake {
		t.Fatal("the fake must never run outside local")
	}
}
