package usecase

import (
	portin "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/inbound"
	portout "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/port/outbound"
)

// UserUsecase implements the inbound port for all user-domain business logic.
type UserUsecase struct {
	repo       portout.IUserRepository
	dukcapil   portout.IDukcapilAdapter
	nikHMACKey string // HMAC key used to hash raw NIKs before persistence
}

// NewUser creates a new UserUsecase and returns the inbound port interface.
func NewUser(
	repo portout.IUserRepository,
	dukcapil portout.IDukcapilAdapter,
	nikHMACKey string,
) portin.IUserUsecase {
	return &UserUsecase{
		repo:       repo,
		dukcapil:   dukcapil,
		nikHMACKey: nikHMACKey,
	}
}
