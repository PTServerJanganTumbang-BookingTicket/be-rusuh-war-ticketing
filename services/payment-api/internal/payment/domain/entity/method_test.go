package entity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogue_OnlyTwoMethodsInOrder(t *testing.T) {
	cat := Catalogue("https://cdn.example/icons", true)
	require.Len(t, cat, 2)
	assert.Equal(t, MethodBankTransfer, cat[0].ID)
	assert.Equal(t, MethodGoPay, cat[1].ID)
}

// Card and over-the-counter are absent because neither honours custom_expiry,
// so the gateway's deadline could outlive the 10-minute window and a virtual
// account would stay payable after the seats were resold.
func TestCatalogue_NoCardOrOverTheCounter(t *testing.T) {
	for _, m := range Catalogue("i", true) {
		assert.NotContains(t, []string{"credit_card", "cstore", "echannel"}, m.ID)
	}
}

func TestCatalogue_BanksOnBankTransferOnly(t *testing.T) {
	cat := Catalogue("https://cdn.example/icons", true)
	require.Len(t, cat[0].Banks, 4)
	assert.Empty(t, cat[1].Banks)

	var codes []string
	for _, b := range cat[0].Banks {
		codes = append(codes, b.Code)
		assert.NotEmpty(t, b.Name)
		assert.True(t, strings.HasPrefix(b.IconURL, "https://cdn.example/icons/"))
	}
	assert.Equal(t, []string{BankBCA, BankBNI, BankBRI, BankCIMB}, codes)
}

// The reversal case Midtrans documents - a settlement flipping to deny minutes
// later - applies only to Permata, Mandiri Bill Payment and Indomaret. Opening
// any of them would need a reversal handler this service does not have.
func TestSupportedBank_ExcludesReversalProneBanks(t *testing.T) {
	for _, code := range []string{BankBCA, BankBNI, BankBRI, BankCIMB} {
		assert.True(t, SupportedBank(code), code)
	}
	for _, code := range []string{"permata", "mandiri", "echannel", "indomaret", "", "BCA"} {
		assert.False(t, SupportedBank(code), code)
	}
}

// enabled is driven by the payment-gateway breaker, so the UI can grey a
// method out rather than letting the customer discover the outage at charge
// time.
func TestCatalogue_EnabledTracksBreaker(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, m := range Catalogue("i", enabled) {
			assert.Equal(t, enabled, m.Enabled, m.ID)
		}
	}
}

func TestCatalogue_IconBaseIsUsedVerbatim(t *testing.T) {
	cat := Catalogue("https://cdn.rusuh.id/icons", true)
	assert.Equal(t, "https://cdn.rusuh.id/icons/bank.svg", cat[0].IconURL)
	assert.Equal(t, "https://cdn.rusuh.id/icons/ewallet.svg", cat[1].IconURL)
}
