package template

import "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"

// catalog maps a type and locale to its email.
//
// A missing entry is reported as ErrNoTemplate rather than falling back to a
// generic message: an email whose body does not match what happened is worse
// than one that is retried after the consumer is redeployed.
var catalog = map[events.Type]map[events.Locale]definition{
	events.TypeAuthMagicLink: {
		events.LocaleID: {
			subject: "Masuk tanpa kata sandi. Link ini hanya bisa dipakai sekali.",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Masuk ke akun kamu</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Masuk ke akun kamu</h2>
<p>Halo, tekan tombol di bawah untuk masuk.</p>
<p style="margin:24px 0;"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Masuk sekarang</a></p>
<p style="color:#6b7280;font-size:14px;">Link berlaku selama {{.TTLMinutes}} menit dan hanya bisa dipakai sekali. Jangan teruskan email ini ke siapa pun.</p>
<p style="color:#6b7280;font-size:14px;">Tombol tidak berfungsi? Salin link ini ke browser:<br><span style="word-break:break-all;">{{.Link}}</span></p>
<p style="color:#6b7280;font-size:14px;">Tidak merasa meminta link ini? Abaikan email ini, akunmu tetap aman.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">Sampai jumpa di depan panggung.<br>Butuh bantuan? Hubungi {{.SupportEmail}}<br>Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Masuk ke akun kamu

Halo, tekan tombol di bawah untuk masuk.

Link: {{.Link}}

Link berlaku selama {{.TTLMinutes}} menit dan hanya bisa dipakai sekali.
Jangan teruskan email ini ke siapa pun.

Tidak merasa meminta link ini? Abaikan email ini, akunmu tetap aman.

---
Sampai jumpa di depan panggung.
Butuh bantuan? Hubungi {{.SupportEmail}}
Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket`,
		},
		events.LocaleEN: {
			subject: "Sign in without a password. This link can only be used once.",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Sign in to your account</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Sign in to your account</h2>
<p>Hello, tap the button below to sign in.</p>
<p style="margin:24px 0;"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Sign in now</a></p>
<p style="color:#6b7280;font-size:14px;">This link is valid for {{.TTLMinutes}} minutes and can only be used once. Do not forward this email to anyone.</p>
<p style="color:#6b7280;font-size:14px;">Button not working? Copy this link to your browser:<br><span style="word-break:break-all;">{{.Link}}</span></p>
<p style="color:#6b7280;font-size:14px;">Didn't request this link? Ignore this email, your account is safe.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">See you in front of the stage.<br>Need help? Contact {{.SupportEmail}}<br>This email was sent automatically, please do not reply. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Sign in to your account

Hello, tap the link below to sign in.

Link: {{.Link}}

This link is valid for {{.TTLMinutes}} minutes and can only be used once.
Do not forward this email to anyone.

Didn't request this link? Ignore this email, your account is safe.

---
See you in front of the stage.
Need help? Contact {{.SupportEmail}}
This email was sent automatically, please do not reply. © Rusuh Booking Tiket`,
		},
	},

	events.TypeAccountVerify: {
		events.LocaleID: {
			subject: "Satu langkah lagi. Link ini berlaku selama 15 menit.",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Verifikasi email kamu</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Satu klik. Akun terverifikasi.</h2>
<p>Halo,</p>
<p>Terima kasih sudah mendaftar di Rusuh. Verifikasi email kamu untuk menyelesaikan pendaftaran dan siap memesan momen live berikutnya. Email yang terverifikasi juga membuka riwayat pesananmu.</p>
<p style="margin:24px 0;"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Verifikasi email</a></p>
<p style="color:#6b7280;font-size:14px;">Link hanya bisa dipakai sekali dan berlaku selama {{.TTLMinutes}} menit. Jika sudah kedaluwarsa, minta link login dari halaman login. Masuk lewat link itu juga memverifikasi emailmu.</p>
<p style="color:#6b7280;font-size:14px;">Tombol tidak berfungsi? Salin link ini ke browser:<br><span style="word-break:break-all;">{{.Link}}</span></p>
<p style="color:#6b7280;font-size:14px;">Tidak merasa mendaftar? Abaikan email ini, akun tidak akan aktif. Jangan bagikan link ini ke siapa pun.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">Sampai jumpa di depan panggung.<br>Butuh bantuan? Hubungi {{.SupportEmail}}<br>Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Satu klik. Akun terverifikasi.

Halo,

Terima kasih sudah mendaftar di Rusuh. Verifikasi email kamu untuk menyelesaikan
pendaftaran dan siap memesan momen live berikutnya.

Link verifikasi: {{.Link}}

Link hanya bisa dipakai sekali dan berlaku selama {{.TTLMinutes}} menit.

Tidak merasa mendaftar? Abaikan email ini, akun tidak akan aktif.

---
Sampai jumpa di depan panggung.
Butuh bantuan? Hubungi {{.SupportEmail}}
Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket`,
		},
		events.LocaleEN: {
			subject: "One more step. This link is valid for 15 minutes.",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Verify your email</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">One click. Account verified.</h2>
<p>Hello,</p>
<p>Thank you for signing up with Rusuh. Verify your email to complete registration and get ready to book your next live moment. A verified email also unlocks your order history.</p>
<p style="margin:24px 0;"><a href="{{.Link}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Verify email</a></p>
<p style="color:#6b7280;font-size:14px;">This link can only be used once and is valid for {{.TTLMinutes}} minutes. If it has expired, request a login link from the login page. Signing in via that link also verifies your email.</p>
<p style="color:#6b7280;font-size:14px;">Button not working? Copy this link to your browser:<br><span style="word-break:break-all;">{{.Link}}</span></p>
<p style="color:#6b7280;font-size:14px;">Didn't sign up? Ignore this email, the account won't be activated. Don't share this link with anyone.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">See you in front of the stage.<br>Need help? Contact {{.SupportEmail}}<br>This email was sent automatically, please do not reply. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `One click. Account verified.

Hello,

Thank you for signing up with Rusuh. Verify your email to complete registration
and get ready to book your next live moment.

Verification link: {{.Link}}

This link can only be used once and is valid for {{.TTLMinutes}} minutes.

Didn't sign up? Ignore this email, the account won't be activated.

---
See you in front of the stage.
Need help? Contact {{.SupportEmail}}
This email was sent automatically, please do not reply. © Rusuh Booking Tiket`,
		},
	},

	events.TypeTicketIssue: {
		events.LocaleID: {
			subject: "E-tiket kamu sudah terbit — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>E-tiket kamu sudah terbit</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">E-tiket kamu sudah terbit</h2>
<p>Pembayaran berhasil. Setiap tiket punya QR code sendiri yang dipindai saat masuk venue. Teruskan tiket ke masing-masing pemegangnya.</p>
<table style="width:100%;border-collapse:collapse;margin:20px 0;font-size:14px;">
<tr><td style="padding:8px 0;color:#6b7280;width:120px;">Event</td><td style="padding:8px 0;"><strong>{{.EventName}}</strong></td></tr>
<tr><td style="padding:8px 0;color:#6b7280;">Waktu</td><td style="padding:8px 0;">{{.EventDate}}</td></tr>
{{if .EventVenue}}<tr><td style="padding:8px 0;color:#6b7280;">Lokasi</td><td style="padding:8px 0;">{{.EventVenue}}</td></tr>{{end}}
<tr><td style="padding:8px 0;color:#6b7280;">Kode booking</td><td style="padding:8px 0;">{{.BookingID}}</td></tr>
</table>
<div style="background:#f3f4f6;border-radius:8px;padding:20px;margin:20px 0;">
<table style="width:100%;border-collapse:collapse;font-size:14px;">
<tr><td style="padding:6px 0;color:#6b7280;width:120px;">Kategori</td><td style="padding:6px 0;"><strong>{{.Category}}</strong></td></tr>
<tr><td style="padding:6px 0;color:#6b7280;">Nomor tiket</td><td style="padding:6px 0;">{{.TicketID}}</td></tr>
<tr><td style="padding:6px 0;color:#6b7280;">NIK pemegang tiket</td><td style="padding:6px 0;">{{.NIKMasked}}</td></tr>
</table>
</div>
<p style="margin:24px 0;"><a href="{{.ETicketLink}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Buka e-tiket</a></p>
<p style="color:#6b7280;font-size:14px;">Bawa identitas sesuai NIK pada tiket. Jangan bagikan QR code selain kepada pemegang tiketnya.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">Sampai jumpa di depan panggung.<br>Butuh bantuan? Hubungi {{.SupportEmail}}<br>Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `E-tiket kamu sudah terbit — {{.EventName}}

Pembayaran berhasil. Tunjukkan QR code saat masuk venue.

Event: {{.EventName}}
Waktu: {{.EventDate}}{{if .EventVenue}}
Lokasi: {{.EventVenue}}{{end}}
Kode booking: {{.BookingID}}

---
Kategori: {{.Category}}
Nomor tiket: {{.TicketID}}
NIK pemegang tiket: {{.NIKMasked}}

Buka e-tiket: {{.ETicketLink}}

Bawa identitas sesuai NIK pada tiket. Jangan bagikan QR code selain kepada pemegang tiketnya.

---
Sampai jumpa di depan panggung.
Butuh bantuan? Hubungi {{.SupportEmail}}
Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket`,
		},
		events.LocaleEN: {
			subject: "Your e-ticket has been issued — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Your e-ticket has been issued</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Your e-ticket has been issued</h2>
<p>Payment successful. Each ticket has its own QR code to be scanned at venue entry. Forward the ticket to each holder.</p>
<table style="width:100%;border-collapse:collapse;margin:20px 0;font-size:14px;">
<tr><td style="padding:8px 0;color:#6b7280;width:120px;">Event</td><td style="padding:8px 0;"><strong>{{.EventName}}</strong></td></tr>
<tr><td style="padding:8px 0;color:#6b7280;">Date</td><td style="padding:8px 0;">{{.EventDate}}</td></tr>
{{if .EventVenue}}<tr><td style="padding:8px 0;color:#6b7280;">Venue</td><td style="padding:8px 0;">{{.EventVenue}}</td></tr>{{end}}
<tr><td style="padding:8px 0;color:#6b7280;">Booking code</td><td style="padding:8px 0;">{{.BookingID}}</td></tr>
</table>
<div style="background:#f3f4f6;border-radius:8px;padding:20px;margin:20px 0;">
<table style="width:100%;border-collapse:collapse;font-size:14px;">
<tr><td style="padding:6px 0;color:#6b7280;width:120px;">Category</td><td style="padding:6px 0;"><strong>{{.Category}}</strong></td></tr>
<tr><td style="padding:6px 0;color:#6b7280;">Ticket number</td><td style="padding:6px 0;">{{.TicketID}}</td></tr>
<tr><td style="padding:6px 0;color:#6b7280;">Holder NIK</td><td style="padding:6px 0;">{{.NIKMasked}}</td></tr>
</table>
</div>
<p style="margin:24px 0;"><a href="{{.ETicketLink}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Open e-ticket</a></p>
<p style="color:#6b7280;font-size:14px;">Bring ID matching the NIK on the ticket. Do not share the QR code with anyone except the ticket holder.</p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">See you in front of the stage.<br>Need help? Contact {{.SupportEmail}}<br>This email was sent automatically, please do not reply. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Your e-ticket has been issued — {{.EventName}}

Payment successful. Show the QR code at venue entry.

Event: {{.EventName}}
Date: {{.EventDate}}{{if .EventVenue}}
Venue: {{.EventVenue}}{{end}}
Booking code: {{.BookingID}}

---
Category: {{.Category}}
Ticket number: {{.TicketID}}
Holder NIK: {{.NIKMasked}}

Open e-ticket: {{.ETicketLink}}

Bring ID matching the NIK on the ticket. Do not share the QR code with anyone except the ticket holder.

---
See you in front of the stage.
Need help? Contact {{.SupportEmail}}
This email was sent automatically, please do not reply. © Rusuh Booking Tiket`,
		},
	},

	events.TypeBookingExpired: {
		events.LocaleID: {
			subject: "Booking kamu kedaluwarsa — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Booking kamu kedaluwarsa</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Booking kamu kedaluwarsa</h2>
<p>Pembayaran belum kami terima sampai batas waktu berakhir, jadi booking ini dibatalkan dan tiketnya dilepas kembali.</p>
<table style="width:100%;border-collapse:collapse;margin:20px 0;font-size:14px;">
<tr><td style="padding:8px 0;color:#6b7280;width:120px;">Kode booking</td><td style="padding:8px 0;"><strong>{{.BookingID}}</strong></td></tr>
<tr><td style="padding:8px 0;color:#6b7280;">Event</td><td style="padding:8px 0;">{{.EventName}}</td></tr>
</table>
<p>Masih ingin hadir? Kamu bisa memesan lagi selama tiket masih tersedia.</p>
<p style="margin:24px 0;"><a href="{{.EventLink}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">Lihat event</a></p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">Sampai jumpa di depan panggung.<br>Butuh bantuan? Hubungi {{.SupportEmail}}<br>Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Booking kamu kedaluwarsa — {{.EventName}}

Batas waktu pembayaran untuk {{.EventName}} sudah lewat.

Pembayaran belum kami terima sampai batas waktu berakhir, jadi booking ini
dibatalkan dan tiketnya dilepas kembali.

Kode booking: {{.BookingID}}
Event: {{.EventName}}

Masih ingin hadir? Kamu bisa memesan lagi selama tiket masih tersedia.

---
Sampai jumpa di depan panggung.
Butuh bantuan? Hubungi {{.SupportEmail}}
Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket`,
		},
		events.LocaleEN: {
			subject: "Your booking expired — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Your booking expired</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Your booking expired</h2>
<p>We did not receive payment before the deadline, so this booking was cancelled and the tickets released.</p>
<table style="width:100%;border-collapse:collapse;margin:20px 0;font-size:14px;">
<tr><td style="padding:8px 0;color:#6b7280;width:120px;">Booking code</td><td style="padding:8px 0;"><strong>{{.BookingID}}</strong></td></tr>
<tr><td style="padding:8px 0;color:#6b7280;">Event</td><td style="padding:8px 0;">{{.EventName}}</td></tr>
</table>
<p>Still want to attend? You can book again while tickets are available.</p>
<p style="margin:24px 0;"><a href="{{.EventLink}}" style="background:#1a1a1a;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;display:inline-block;font-weight:500;">View event</a></p>
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">See you in front of the stage.<br>Need help? Contact {{.SupportEmail}}<br>This email was sent automatically, please do not reply. © Rusuh Booking Tiket</p>
</body>
</html>`,
			text: `Your booking expired — {{.EventName}}

Payment deadline for {{.EventName}} has passed.

We did not receive payment before the deadline, so this booking was cancelled
and the tickets released.

Booking code: {{.BookingID}}
Event: {{.EventName}}

Still want to attend? You can book again while tickets are available.

---
See you in front of the stage.
Need help? Contact {{.SupportEmail}}
This email was sent automatically, please do not reply. © Rusuh Booking Tiket`,
		},
	},

	// FR-28a: three conditions, one type. The body must differ per condition —
	// telling a customer whose money was taken that "payment did not complete"
	// is both false and alarming. FR-28b makes condition (c) a mandatory
	// email, not a log line.
	events.TypePaymentFailedNotice: {
		events.LocaleID: {
			subject: "Pembayaran tidak berhasil — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Pembayaran tidak berhasil</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Pembayaran tidak berhasil</h2>
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
{{if .OrderID}}<p style="font-size:13px;color:#6b7280;margin-top:16px;">Nomor referensi: {{.OrderID}}</p>{{end}}
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">Sampai jumpa di depan panggung.<br>Butuh bantuan? Hubungi {{.SupportEmail}}<br>Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket</p>
</body>
</html>`,
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
Nomor referensi: {{.OrderID}}{{end}}

---
Sampai jumpa di depan panggung.
Butuh bantuan? Hubungi {{.SupportEmail}}
Email ini dikirim otomatis, mohon tidak membalas. © Rusuh Booking Tiket`,
		},
		events.LocaleEN: {
			subject: "Payment unsuccessful — {{.EventName}}",
			html: `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Payment unsuccessful</title>
</head>
<body style="font-family:system-ui,-apple-system,sans-serif;line-height:1.6;color:#1a1a1a;max-width:560px;margin:0 auto;padding:24px;background:#f9fafb;">
<div style="background:#fff;border-radius:8px;padding:32px;margin:16px 0;">
<h2 style="margin:0 0 16px;font-size:20px;color:#111827;">Payment unsuccessful</h2>
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
{{if .OrderID}}<p style="font-size:13px;color:#6b7280;margin-top:16px;">Reference: {{.OrderID}}</p>{{end}}
</div>
<hr style="border:0;border-top:1px solid #e5e5e5;margin:24px 0;">
<p style="font-size:12px;color:#6b7280;text-align:center;">See you in front of the stage.<br>Need help? Contact {{.SupportEmail}}<br>This email was sent automatically, please do not reply. © Rusuh Booking Tiket</p>
</body>
</html>`,
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
Reference: {{.OrderID}}{{end}}

---
See you in front of the stage.
Need help? Contact {{.SupportEmail}}
This email was sent automatically, please do not reply. © Rusuh Booking Tiket`,
		},
	},
}
