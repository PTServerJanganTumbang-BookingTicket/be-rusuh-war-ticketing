module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/queue

go 1.26.11

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/alicebob/miniredis/v2 v2.40.0
	github.com/aws/aws-lambda-go v1.55.1
	github.com/awslabs/aws-lambda-go-api-proxy v0.16.2
	github.com/cucumber/godog v0.15.1
	github.com/jackc/pgx/v5 v5.9.2
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cucumber/gherkin/go/v26 v26.2.0 // indirect
	github.com/cucumber/messages/go/v21 v21.0.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/gofrs/uuid v4.3.1+incompatible // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.4 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/oklog/ulid/v2 v2.1.2 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
