package usecase

import (
	"testing"

	mock_outbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/_mock/user/port/outbound"
	"go.uber.org/mock/gomock"
)

// newTestUsecase wires a UserUsecase backed by mocks for use in tests.
func newTestUsecase(t *testing.T) (*UserUsecase, *mock_outbound.MockIUserRepository, *mock_outbound.MockIDukcapilAdapter) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIUserRepository(ctrl)
	dukcapil := mock_outbound.NewMockIDukcapilAdapter(ctrl)
	uc := NewUser(repo, dukcapil, "test-hmac-key").(*UserUsecase)
	return uc, repo, dukcapil
}
