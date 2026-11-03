module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/notification

go 1.26.0

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/aws/aws-lambda-go v1.55.1
	github.com/aws/aws-sdk-go-v2/config v1.28.6
	github.com/aws/aws-sdk-go-v2/service/sesv2 v1.36.2
	github.com/redis/go-redis/v9 v9.7.0
	github.com/stretchr/testify v1.11.1
	go.uber.org/mock v0.6.0
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
