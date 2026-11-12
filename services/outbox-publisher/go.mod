module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/outbox-publisher

go 1.26.11

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/alicebob/miniredis/v2 v2.40.0
	github.com/aws/aws-lambda-go v1.55.1
	github.com/aws/aws-sdk-go-v2/config v1.28.6
	github.com/aws/aws-sdk-go-v2/service/sqs v1.37.2
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.7.3
	github.com/stretchr/testify v1.11.1
	go.uber.org/mock v0.6.0
)

require (
	github.com/aws/aws-sdk-go-v2 v1.32.6 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.47 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.25 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.25 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.24.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.28.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.2 // indirect
	github.com/aws/smithy-go v1.22.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
