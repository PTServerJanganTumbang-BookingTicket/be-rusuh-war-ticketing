package template

import (
	"fmt"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
)

// layout wraps a body fragment in a minimal HTML document.
//
// Deliberately plain: inline styles only, no external CSS or images, no
// tracking pixel. Mail clients strip most of it anyway, and an e-ticket that
// renders in a text-only client is worth more than one that looks good in Gmail
// and is unreadable elsewhere.
const layout = `<!doctype html>
<html><body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.5;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px">
%s
<hr style="border:0;border-top:1px solid #e5e5e5;margin:32px 0">
<p style="font-size:12px;color:#6b7280">%s</p>
</body></html>`

func wrap(body, footer string) string {
	return fmt.Sprintf(layout, body, footer)
}

// catalog maps a type and locale to its email.
//
// A missing entry is reported as ErrNoTemplate rather than falling back to a
// generic message: an email whose body does not match what happened is worse
// than one that is retried after the consumer is redeployed.
var catalog = map[events.Type]map[events.Locale]definition{
	events.TypeAuthMagicLink: {
		events.LocaleID: {
			subject: "Tautan masuk Rusuh Booking Tiket",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Masuk ke akun Anda</h2>
<p>Klik tombol di bawah untuk masuk. Tautan ini berlaku {{.TTLMinutes}} menit dan hanya dapat dipakai satu kali.</p>
<p style="margin:24px 0"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Masuk sekarang</a></p>
<p>Jika Anda tidak meminta tautan ini, abaikan email ini. Tidak ada perubahan pada akun Anda.</p>`,
				"Email ini dikirim otomatis. Jangan teruskan tautan di atas kepada siapa pun."),
			text: `Masuk ke akun Anda

Buka tautan ini untuk masuk (berlaku {{.TTLMinutes}} menit, sekali pakai):
{{.Link}}

Jika Anda tidak meminta tautan ini, abaikan email ini.`,
		},
		events.LocaleEN: {
			subject: "Your Rusuh Booking Tiket sign-in link",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Sign in to your account</h2>
<p>Use the button below to sign in. The link is valid for {{.TTLMinutes}} minutes and works once.</p>
<p style="margin:24px 0"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Sign in</a></p>
<p>If you did not request this, ignore this email. Nothing has changed on your account.</p>`,
				"This is an automated message. Do not forward the link above to anyone."),
			text: `Sign in to your account

Open this link to sign in (valid for {{.TTLMinutes}} minutes, single use):
{{.Link}}

If you did not request this, ignore this email.`,
		},
	},

	events.TypeAccountVerify: {
		events.LocaleID: {
			subject: "Konfirmasi alamat email Anda",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Konfirmasi email Anda</h2>
<p>Konfirmasi alamat ini untuk melanjutkan registrasi.</p>
<p style="margin:24px 0"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Konfirmasi email</a></p>
<p>Anda tetap dapat membeli tiket tanpa konfirmasi ini. Tautan berlaku {{.TTLMinutes}} menit.</p>`,
				"Email ini dikirim otomatis."),
			text: `Konfirmasi email Anda

Konfirmasi alamat ini untuk melanjutkan registrasi.
Buka tautan ini (berlaku {{.TTLMinutes}} menit):
{{.Link}}

Anda tetap dapat membeli tiket tanpa konfirmasi ini.`,
		},
		events.LocaleEN: {
			subject: "Confirm your email address",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Confirm your email</h2>
<p>Your account is ready. Confirm this address to unlock your order history.</p>
<p style="margin:24px 0"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Confirm email</a></p>
<p>You can still buy tickets without confirming. The link is valid for {{.TTLMinutes}} minutes.</p>`,
				"This is an automated message."),
			text: `Confirm your email

Your account is ready. Open this link to confirm your address
(valid for {{.TTLMinutes}} minutes):
{{.Link}}

You can still buy tickets without confirming.`,
		},
	},

	events.TypeTicketIssue: {
		events.LocaleID: {
			subject: "E-tiket Anda — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">E-tiket Anda</h2>
<p><strong>{{.EventName}}</strong><br>{{.EventDate}}{{if .EventVenue}} · {{.EventVenue}}{{end}}</p>
<table style="border-collapse:collapse;margin:20px 0;font-size:14px">
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">Kategori</td><td style="padding:4px 0"><strong>{{.Category}}</strong></td></tr>
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">NIK pemegang</td><td style="padding:4px 0">{{.NIKMasked}}</td></tr>
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">Kode tiket</td><td style="padding:4px 0">{{.TicketID}}</td></tr>
</table>
<p style="margin:24px 0"><a href="{{.ETicketLink}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Buka e-tiket</a></p>
<p>Tunjukkan QR code pada e-tiket beserta identitas pemegang saat masuk venue.</p>`,
				"Tiket ini terikat pada NIK pemegang dan tidak dapat dipindahtangankan."),
			text: `E-tiket Anda — {{.EventName}}

{{.EventDate}}{{if .EventVenue}} · {{.EventVenue}}{{end}}
Kategori: {{.Category}}
NIK pemegang: {{.NIKMasked}}
Kode tiket: {{.TicketID}}

Buka e-tiket Anda: {{.ETicketLink}}

Tunjukkan QR code beserta identitas pemegang saat masuk venue.`,
		},
		events.LocaleEN: {
			subject: "Your e-ticket — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Your e-ticket</h2>
<p><strong>{{.EventName}}</strong><br>{{.EventDate}}{{if .EventVenue}} · {{.EventVenue}}{{end}}</p>
<table style="border-collapse:collapse;margin:20px 0;font-size:14px">
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">Category</td><td style="padding:4px 0"><strong>{{.Category}}</strong></td></tr>
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">Holder NIK</td><td style="padding:4px 0">{{.NIKMasked}}</td></tr>
<tr><td style="padding:4px 16px 4px 0;color:#6b7280">Ticket code</td><td style="padding:4px 0">{{.TicketID}}</td></tr>
</table>
<p style="margin:24px 0"><a href="{{.ETicketLink}}" style="background:#1a1a1a;color:#fff;padding:12px 20px;border-radius:6px;text-decoration:none;display:inline-block">Open e-ticket</a></p>
<p>Show the QR code on your e-ticket together with the holder's ID at the venue.</p>`,
				"This ticket is bound to the holder's NIK and is not transferable."),
			text: `Your e-ticket — {{.EventName}}

{{.EventDate}}{{if .EventVenue}} · {{.EventVenue}}{{end}}
Category: {{.Category}}
Holder NIK: {{.NIKMasked}}
Ticket code: {{.TicketID}}

Open your e-ticket: {{.ETicketLink}}

Show the QR code together with the holder's ID at the venue.`,
		},
	},

	events.TypeBookingExpired: {
		events.LocaleID: {
			subject: "Pesanan Anda kedaluwarsa — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Pesanan kedaluwarsa</h2>
<p>Pembayaran untuk pesanan <strong>{{.BookingID}}</strong> ({{.EventName}}) tidak selesai dalam batas waktu, sehingga pesanan dibatalkan dan kursi dilepas kembali.</p>
<p>Tidak ada dana yang terpotong. Anda dapat mencoba memesan kembali selama tiket masih tersedia.</p>`,
				"Email ini dikirim otomatis."),
			text: `Pesanan kedaluwarsa

Pembayaran untuk pesanan {{.BookingID}} ({{.EventName}}) tidak selesai dalam
batas waktu, sehingga pesanan dibatalkan dan kursi dilepas kembali.

Tidak ada dana yang terpotong.`,
		},
		events.LocaleEN: {
			subject: "Your booking expired — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Booking expired</h2>
<p>Payment for booking <strong>{{.BookingID}}</strong> ({{.EventName}}) was not completed in time, so the booking was cancelled and the seats released.</p>
<p>You have not been charged. You can book again while tickets remain.</p>`,
				"This is an automated message."),
			text: `Booking expired

Payment for booking {{.BookingID}} ({{.EventName}}) was not completed in time,
so the booking was cancelled and the seats released.

You have not been charged.`,
		},
	},

	// FR-28a: three conditions, one type. The body must differ per condition —
	// telling a customer whose money was taken that "payment did not complete"
	// is both false and alarming. FR-28b makes condition (c) a mandatory
	// email, not a log line.
	events.TypePaymentFailedNotice: {
		events.LocaleID: {
			subject: "Pembayaran tidak berhasil — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Pembayaran tidak berhasil</h2>
{{if .IsDisputed}}
<p>Kami menerima pembayaran Anda untuk pesanan <strong>{{.BookingID}}</strong> ({{.EventName}}), tetapi pembayaran itu tiba setelah batas waktu dan pesanan sudah dibatalkan.</p>
<p><strong>Dana Anda{{if .AmountText}} sebesar {{.AmountText}}{{end}} telah diterima dan pengembalian dana sedang diproses.</strong> Tim kami akan menghubungi Anda bila diperlukan informasi tambahan.</p>
{{else if .IsTTLElapsed}}
<p>Pembayaran untuk pesanan <strong>{{.BookingID}}</strong> ({{.EventName}}) tidak kami terima dalam batas waktu, sehingga pesanan dibatalkan dan kursi dilepas kembali.</p>
<p>Tidak ada dana yang terpotong. Anda dapat mencoba memesan kembali selama tiket masih tersedia.</p>
{{else}}
<p>Pembayaran untuk pesanan <strong>{{.BookingID}}</strong> ({{.EventName}}) ditolak oleh penyedia pembayaran, sehingga pesanan dibatalkan.</p>
<p>Tidak ada dana yang terpotong. Anda dapat mencoba lagi dengan metode pembayaran lain selama tiket masih tersedia.</p>
{{end}}
{{if .OrderID}}<p style="font-size:13px;color:#6b7280">Nomor referensi: {{.OrderID}}</p>{{end}}`,
				"Email ini dikirim otomatis."),
			text: `Pembayaran tidak berhasil — {{.EventName}}

{{if .IsDisputed}}Kami menerima pembayaran Anda untuk pesanan {{.BookingID}}, tetapi
pembayaran itu tiba setelah batas waktu dan pesanan sudah dibatalkan.

Dana Anda{{if .AmountText}} sebesar {{.AmountText}}{{end}} telah diterima dan pengembalian dana
sedang diproses.
{{else if .IsTTLElapsed}}Pembayaran untuk pesanan {{.BookingID}} tidak kami terima dalam batas
waktu, sehingga pesanan dibatalkan dan kursi dilepas kembali.

Tidak ada dana yang terpotong.
{{else}}Pembayaran untuk pesanan {{.BookingID}} ditolak oleh penyedia pembayaran,
sehingga pesanan dibatalkan.

Tidak ada dana yang terpotong.
{{end}}{{if .OrderID}}
Nomor referensi: {{.OrderID}}{{end}}`,
		},
		events.LocaleEN: {
			subject: "Payment unsuccessful — {{.EventName}}",
			html: wrap(
				`<h2 style="margin:0 0 16px;font-size:20px">Payment unsuccessful</h2>
{{if .IsDisputed}}
<p>We received your payment for booking <strong>{{.BookingID}}</strong> ({{.EventName}}), but it arrived after the deadline and the booking had already been cancelled.</p>
<p><strong>Your funds{{if .AmountText}} of {{.AmountText}}{{end}} have been received and a refund is being processed.</strong> We will contact you if we need anything further.</p>
{{else if .IsTTLElapsed}}
<p>We did not receive payment for booking <strong>{{.BookingID}}</strong> ({{.EventName}}) in time, so the booking was cancelled and the seats released.</p>
<p>You have not been charged. You can book again while tickets remain.</p>
{{else}}
<p>Payment for booking <strong>{{.BookingID}}</strong> ({{.EventName}}) was declined by the payment provider, so the booking was cancelled.</p>
<p>You have not been charged. You can try again with another payment method while tickets remain.</p>
{{end}}
{{if .OrderID}}<p style="font-size:13px;color:#6b7280">Reference: {{.OrderID}}</p>{{end}}`,
				"This is an automated message."),
			text: `Payment unsuccessful — {{.EventName}}

{{if .IsDisputed}}We received your payment for booking {{.BookingID}}, but it arrived after
the deadline and the booking had already been cancelled.

Your funds{{if .AmountText}} of {{.AmountText}}{{end}} have been received and a refund is being
processed.
{{else if .IsTTLElapsed}}We did not receive payment for booking {{.BookingID}} in time, so the
booking was cancelled and the seats released.

You have not been charged.
{{else}}Payment for booking {{.BookingID}} was declined by the payment provider,
so the booking was cancelled.

You have not been charged.
{{end}}{{if .OrderID}}
Reference: {{.OrderID}}{{end}}`,
		},
	},
}
