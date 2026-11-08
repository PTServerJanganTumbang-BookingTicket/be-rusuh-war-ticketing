module github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl

go 1.26.10

require (
	github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg v0.0.0
	github.com/cucumber/godog v0.15.1
	github.com/jackc/pgx/v5 v5.9.2
)

require (
	github.com/cucumber/gherkin/go/v26 v26.2.0 // indirect
	github.com/cucumber/messages/go/v21 v21.0.1 // indirect
	github.com/gofrs/uuid v4.3.1+incompatible // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.4 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// Shared library lives in this monorepo; resolved locally (no proxy publish).
replace github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg => ../../pkg
