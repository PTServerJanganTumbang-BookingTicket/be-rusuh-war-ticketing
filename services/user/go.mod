module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user

go 1.26.0

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/aws/aws-lambda-go v1.55.1
	github.com/awslabs/aws-lambda-go-api-proxy v0.16.2
	github.com/go-playground/validator/v10 v10.27.0
	github.com/jackc/pgx/v5 v5.7.2
	github.com/redis/go-redis/v9 v9.7.0
	github.com/stretchr/testify v1.11.1
	go.uber.org/mock v0.6.0
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
