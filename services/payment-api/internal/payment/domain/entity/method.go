package entity

// Payment method ids. The value is Midtrans's own payment_type, so the id the
// client sends back as `method` on §5.2 needs no translation.
const (
	MethodBankTransfer = "bank_transfer"
	MethodGoPay        = "gopay"
)

// Bank codes opened in the POC.
//
// Only four, and the reason is a failure class rather than effort: Midtrans
// documents the reversal case (settlement flipping to deny within 1-5 minutes,
// money returned to the customer) ONLY for Permata, Mandiri Bill Payment and
// Indomaret. None of those is opened, so that class cannot occur and no
// reversal handler is needed (api-contracts §5.1).
const (
	BankBCA  = "bca"
	BankBNI  = "bni"
	BankBRI  = "bri"
	BankCIMB = "cimb"
)

// Bank is one selectable bank under bank_transfer.
type Bank struct {
	Code    string
	Name    string
	IconURL string
}

// Method is one selectable payment method.
type Method struct {
	ID          string
	Name        string
	Description string
	IconURL     string
	Enabled     bool
	// Banks is populated for bank_transfer only.
	Banks []Bank
}

// Catalogue is the POC's two methods, in display order.
//
// Credit card and Over the Counter are excluded for the same reason, and it is
// not scope-trimming: neither can honour the 10-minute payment TTL at the
// gateway. custom_expiry does not apply to cards (Midtrans states this
// explicitly), and OTC requires the customer to reach a physical store. Both
// methods below accept custom_expiry, so Midtrans's window and ours end on the
// same second for every live method (api-contracts §5.1).
//
// enabled is false while the payment-gateway breaker is open, so the UI greys
// the choice out instead of letting the customer discover it at charge time.
func Catalogue(iconBase string, enabled bool) []Method {
	return []Method{
		{
			ID:          MethodBankTransfer,
			Name:        "Transfer Bank / Virtual Account",
			Description: "Verifikasi otomatis, tanpa unggah bukti.",
			IconURL:     iconBase + "/bank.svg",
			Enabled:     enabled,
			Banks: []Bank{
				{Code: BankBCA, Name: "BCA", IconURL: iconBase + "/bca.svg"},
				{Code: BankBNI, Name: "BNI", IconURL: iconBase + "/bni.svg"},
				{Code: BankBRI, Name: "BRI", IconURL: iconBase + "/bri.svg"},
				{Code: BankCIMB, Name: "CIMB Niaga", IconURL: iconBase + "/cimb.svg"},
			},
		},
		{
			ID:          MethodGoPay,
			Name:        "E-Wallet (GoPay / QRIS)",
			Description: "Scan QR atau buka aplikasi GoPay.",
			IconURL:     iconBase + "/ewallet.svg",
			Enabled:     enabled,
		},
	}
}

// SupportedBank reports whether a bank code is opened.
func SupportedBank(code string) bool {
	switch code {
	case BankBCA, BankBNI, BankBRI, BankCIMB:
		return true
	default:
		return false
	}
}
