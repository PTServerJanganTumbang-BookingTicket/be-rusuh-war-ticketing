package entity

// MethodSwitch identifies what an operator turned off: a whole method, or one
// bank under it.
//
// A zero BankCode means the method as a whole, and that is a different thing
// from a bank whose code happens to be empty — which cannot occur, because
// SupportedBank only admits the four opened codes.
type MethodSwitch struct {
	MethodID string
	BankCode string
}

// Disabled is the set of methods and banks currently switched off.
//
// A set rather than a list of rows: the adapter has already reduced the
// append-only switch log to its latest state per method and bank, and nothing
// above this point needs to know a row existed. It is also safe when empty,
// which is the normal case and the case a failed read falls back to.
type Disabled map[MethodSwitch]struct{}

// MethodOff reports whether the whole method is switched off.
func (d Disabled) MethodOff(methodID string) bool {
	_, off := d[MethodSwitch{MethodID: methodID}]
	return off
}

// BankOff reports whether one bank under a method is switched off.
//
// A bank is also off when its method is off, so a caller cannot offer a bank
// under a method nobody can pay with. Checking only the bank would let
// `bank_transfer` off + `bca` on read as "BCA is available".
func (d Disabled) BankOff(methodID, bankCode string) bool {
	if d.MethodOff(methodID) {
		return true
	}
	_, off := d[MethodSwitch{MethodID: methodID, BankCode: bankCode}]
	return off
}

// ApplyDisabled masks the catalogue with the operator's switches.
//
// Two different shapes, because the wire has two:
//
//   - A disabled METHOD stays in the list with Enabled false. §5.1 already has
//     that field, the frontend already greys it out, and a customer who sees
//     "Transfer Bank (unavailable)" understands more than one who sees a list
//     that silently lost an option they used last week.
//   - A disabled BANK is REMOVED from Banks. Bank on the wire is
//     {code, name, icon_url} with no enabled field, so there is nothing to set
//     — and adding one would be a contract change for an incident tool.
//
// A method whose every bank is switched off becomes Enabled false rather than
// an empty bank list: "choose a bank" with no banks is a dead end, and the
// frontend's own check is "is there a bank step", which an empty array answers
// ambiguously.
//
// The catalogue is never ADDED to. Everything here either stays as the code
// declared it or becomes less available, which is what makes a stray row in
// the switch table harmless.
func ApplyDisabled(methods []Method, d Disabled) []Method {
	if len(d) == 0 {
		return methods
	}
	out := make([]Method, 0, len(methods))
	for _, m := range methods {
		if d.MethodOff(m.ID) {
			m.Enabled = false
			m.Banks = nil
			out = append(out, m)
			continue
		}
		if len(m.Banks) > 0 {
			banks := make([]Bank, 0, len(m.Banks))
			for _, b := range m.Banks {
				if !d.BankOff(m.ID, b.Code) {
					banks = append(banks, b)
				}
			}
			if len(banks) == 0 {
				m.Enabled = false
				m.Banks = nil
				out = append(out, m)
				continue
			}
			m.Banks = banks
		}
		out = append(out, m)
	}
	return out
}
