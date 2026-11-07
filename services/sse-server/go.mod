module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/sse-server

go 1.26.10

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/alicebob/miniredis/v2 v2.40.0
	github.com/cucumber/godog v0.15.1
	github.com/redis/go-redis/v9 v9.7.3
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
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
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
