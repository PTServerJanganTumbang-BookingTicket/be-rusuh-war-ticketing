package memory

import (
	"context"
	"errors"
	"testing"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

func TestBookingRepository_SaveAndFindByID(t *testing.T) {
	repo := NewBookingRepository()
	ctx := context.Background()

	booking := entity.Booking{ID: "RB-001", Status: entity.StatusPendingPayment, Total: 1500000}

	saved, err := repo.Save(ctx, booking)
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if saved.ID != "RB-001" {
		t.Errorf("Save returned ID = %q, want RB-001", saved.ID)
	}

	got, err := repo.FindByID(ctx, "RB-001")
	if err != nil {
		t.Fatalf("FindByID returned error: %v", err)
	}
	if got.ID != "RB-001" || got.Total != 1500000 {
		t.Errorf("FindByID returned %+v, want the saved booking", got)
	}
}

func TestBookingRepository_FindByID_notFound(t *testing.T) {
	repo := NewBookingRepository()

	_, err := repo.FindByID(context.Background(), "RB-missing")
	if err == nil {
		t.Fatal("FindByID on unknown id returned nil error, want not-found")
	}

	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error is not *apperror.AppError: %v", err)
	}
	if appErr.Code != "40400" {
		t.Errorf("error code = %q, want 40400 (not found)", appErr.Code)
	}
}

func TestBookingRepository_Save_overwrites(t *testing.T) {
	repo := NewBookingRepository()
	ctx := context.Background()

	_, _ = repo.Save(ctx, entity.Booking{ID: "RB-dup", Total: 100})
	_, _ = repo.Save(ctx, entity.Booking{ID: "RB-dup", Total: 200})

	got, err := repo.FindByID(ctx, "RB-dup")
	if err != nil {
		t.Fatalf("FindByID returned error: %v", err)
	}
	if got.Total != 200 {
		t.Errorf("Total = %d, want 200 (latest Save wins)", got.Total)
	}
}
