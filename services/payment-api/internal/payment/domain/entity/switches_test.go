package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const iconBase = "https://cdn.rusuh.id/icons"

func catalogue() []Method { return Catalogue(iconBase, true) }

func TestApplyDisabled_NoSwitchesIsTheCatalogueUntouched(t *testing.T) {
	assert.Equal(t, catalogue(), ApplyDisabled(catalogue(), nil))
	assert.Equal(t, catalogue(), ApplyDisabled(catalogue(), Disabled{}))
}

// A switched-off METHOD stays in the list with enabled false. §5.1 already has
// that field and the frontend already greys it out, so a customer sees
// "unavailable" rather than a list that silently lost an option.
func TestApplyDisabled_ASwitchedOffMethodStaysButIsNotEnabled(t *testing.T) {
	out := ApplyDisabled(catalogue(), offMethodSet(MethodGoPay))

	require.Len(t, out, 2, "the method is not removed from the list")
	byID := index(out)
	assert.False(t, byID[MethodGoPay].Enabled)
	assert.True(t, byID[MethodBankTransfer].Enabled, "the other method is untouched")
}

// A switched-off BANK is removed from banks[]. Bank on the wire is
// {code, name, icon_url} with no enabled field, so there is nothing to set —
// and adding one would be a contract change for an incident tool.
func TestApplyDisabled_ASwitchedOffBankIsRemovedFromTheList(t *testing.T) {
	out := ApplyDisabled(catalogue(), offBankSet(MethodBankTransfer, BankBCA))

	bt := index(out)[MethodBankTransfer]
	assert.True(t, bt.Enabled, "the method itself is still payable through the other banks")
	codes := bankCodes(bt)
	assert.NotContains(t, codes, BankBCA)
	assert.Equal(t, []string{BankBNI, BankBRI, BankCIMB}, codes)
}

// "Choose a bank" with no banks is a dead end, and an empty array answers the
// frontend's own question — "is there a bank step" — ambiguously.
func TestApplyDisabled_EveryBankOffDisablesTheMethodRatherThanEmptyingIt(t *testing.T) {
	all := Disabled{}
	for _, b := range []string{BankBCA, BankBNI, BankBRI, BankCIMB} {
		all[MethodSwitch{MethodID: MethodBankTransfer, BankCode: b}] = struct{}{}
	}

	bt := index(ApplyDisabled(catalogue(), all))[MethodBankTransfer]
	assert.False(t, bt.Enabled)
	assert.Empty(t, bt.Banks, "no bank step at all, rather than an empty one")
}

// Switching off the method must also switch off every bank under it. Reading
// only the bank row would let "bank_transfer off, bca not mentioned" render as
// "BCA is available".
func TestApplyDisabled_AMethodOffTakesItsBanksWithIt(t *testing.T) {
	bt := index(ApplyDisabled(catalogue(), offMethodSet(MethodBankTransfer)))[MethodBankTransfer]
	assert.False(t, bt.Enabled)
	assert.Empty(t, bt.Banks)
}

func TestDisabled_BankOffIsTrueWhenTheMethodIsOff(t *testing.T) {
	d := offMethodSet(MethodBankTransfer)
	assert.True(t, d.BankOff(MethodBankTransfer, BankBCA))
	assert.True(t, d.MethodOff(MethodBankTransfer))
	assert.False(t, d.MethodOff(MethodGoPay))
	assert.False(t, d.BankOff(MethodGoPay, ""))
}

// A row naming something the code does not know is inert. That is what makes
// the table safe: it can only ever subtract from the catalogue, so a typo or a
// row for a method that was never built cannot advertise anything.
func TestApplyDisabled_ARowForAnUnknownMethodChangesNothing(t *testing.T) {
	out := ApplyDisabled(catalogue(), Disabled{
		MethodSwitch{MethodID: "credit_card"}:                        {},
		MethodSwitch{MethodID: "bank_transfer", BankCode: "permata"}: {},
	})
	assert.Equal(t, catalogue(), out)
}

// The input is not mutated: Catalogue returns fresh values per call today, but
// a caller that memoised it would otherwise find its copy edited.
func TestApplyDisabled_DoesNotMutateItsInput(t *testing.T) {
	in := catalogue()
	_ = ApplyDisabled(in, offBankSet(MethodBankTransfer, BankBCA))
	assert.Len(t, in[0].Banks, 4, "the caller's slice still has every bank")
}

// ── helpers ─────────────────────────────────────────────────────────────────

func offMethodSet(id string) Disabled {
	return Disabled{MethodSwitch{MethodID: id}: {}}
}

func offBankSet(methodID, bank string) Disabled {
	return Disabled{MethodSwitch{MethodID: methodID, BankCode: bank}: {}}
}

func index(methods []Method) map[string]Method {
	out := make(map[string]Method, len(methods))
	for _, m := range methods {
		out[m.ID] = m
	}
	return out
}

func bankCodes(m Method) []string {
	out := make([]string, 0, len(m.Banks))
	for _, b := range m.Banks {
		out = append(out, b.Code)
	}
	return out
}
