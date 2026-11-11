package nikformat_test

import (
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/nikformat"
)

func TestNormalizeKeepsOnlyTheDigits(t *testing.T) {
	cases := map[string]string{
		"3171000000000022":     "3171000000000022",
		"3171 0000 0000 0022":  "3171000000000022",
		"3171-0000-0000-0022":  "3171000000000022",
		"3171.0000.0000.0022":  "3171000000000022",
		"  3171000000000022  ": "3171000000000022",
		"3171000000000022\n":   "3171000000000022",
		"abc":                  "",
		"":                     "",
	}
	for in, want := range cases {
		if got := nikformat.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidFormatWantsExactlySixteenDigits(t *testing.T) {
	for in, want := range map[string]bool{
		"3171000000000022":  true,
		"317100000000002":   false,
		"31710000000000222": false,
		"":                  false,
	} {
		if got := nikformat.IsValidFormat(in); got != want {
			t.Errorf("IsValidFormat(%q) = %v, want %v", in, got, want)
		}
	}
}

// Booking checks the format and nik-verify normalises again: both must read the same text the same way, or the
// on-blur check and Pay would disagree about one NIK.
func TestAFormattedNIKIsValidOnlyAfterNormalising(t *testing.T) {
	const typed = "3171 0000 0000 0022"
	if nikformat.IsValidFormat(typed) {
		t.Fatal("the typed text has 19 characters and must not pass before Normalize")
	}
	if !nikformat.IsValidFormat(nikformat.Normalize(typed)) {
		t.Fatal("the normalised text must pass")
	}
}
