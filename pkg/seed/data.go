// Package seed fills a local or dev stack with sample events in Jakarta: the Postgres catalog (events,
// ticket_categories) and the Redis sale state (queue:{ev}, stock:{ev}:<category>).
//
// The events are fictional; the venues are real, and so are the pictures (photos of the venues, free licence, hotlinked).
// docs/seed-data.md is the plan. Run it with `make seed`.
package seed

import "time"

// State values of queue:{ev}.state (docs/redis-keys.md).
const (
	StateScheduled  = "SCHEDULED"
	StateOpen       = "OPEN"
	StateFullBooked = "FULL_BOOKED"
	StateClosed     = "CLOSED"
)

// Category is one ticket tier of an event.
type Category struct {
	ID          string // globally unique: ticket_categories.id is the primary key
	Name        string
	Description string
	Price       int64 // IDR
	Capacity    int64
	Available   int64 // stock at seed time
	MaxPerOrder int
}

// Event is one sample event with its sale state.
type Event struct {
	ID          string
	Name        string
	Category    string
	Venue       string
	City        string
	Date        string // YYYY-MM-DD, Asia/Jakarta
	Time        string // HH:MM, Asia/Jakarta
	Description string
	ImageURL    string // Wikimedia Commons thumbnail; credit in ImageCredit
	ImageCredit string // author, licence, file: what the frontend should show next to the picture
	SaleStarts  time.Time
	State       string
	Categories  []Category
}

const commons = "https://thumb.wikimedia.org/wikipedia/commons/thumb/"

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// JakartaEvents returns the five sample events, each in a different sale state.
func JakartaEvents() []Event {
	return []Event{
		{
			ID: "evt_jkt_istora_nightfall", Name: "Nightfall Live di Istora", Category: "LIVE MUSIC",
			Venue: "Istora Gelora Bung Karno, Senayan", City: "Jakarta", Date: "2026-12-19", Time: "19:30",
			Description: "Satu malam penuh musik live di arena indoor ikonik Senayan. Tata suara dan cahaya penuh, pintu dibuka dua jam sebelum pertunjukan.",
			ImageURL:    commons + "c/ca/Istora_Gelora_Bung_Karno%2C_3_March_2024.jpg/1280px-Istora_Gelora_Bung_Karno%2C_3_March_2024.jpg",
			ImageCredit: "RasyaAbhirama13, CC BY-SA 4.0, Wikimedia Commons: Istora Gelora Bung Karno, 3 March 2024.jpg",
			SaleStarts:  utc(2026, 11, 10, 3, 0), State: StateOpen,
			Categories: []Category{
				{ID: "cat_istora_vip", Name: "VIP", Description: "Area terdekat dengan panggung.", Price: 1_500_000, Capacity: 300, Available: 300, MaxPerOrder: 3},
				{ID: "cat_istora_festival", Name: "Festival", Description: "Area berdiri di lantai arena.", Price: 750_000, Capacity: 3000, Available: 3000, MaxPerOrder: 3},
				{ID: "cat_istora_tribune", Name: "Tribune", Description: "Tempat duduk di tribun.", Price: 450_000, Capacity: 3000, Available: 3000, MaxPerOrder: 3},
			},
		},
		{
			ID: "evt_jkt_gbk_garuda_fest", Name: "Garuda Stadium Fest", Category: "LIVE MUSIC",
			Venue: "Stadion Utama Gelora Bung Karno, Senayan", City: "Jakarta", Date: "2027-01-30", Time: "18:00",
			Description: "Festival musik satu hari di stadion terbesar Indonesia, dengan panggung utama dan layar raksasa. Penjualan tiket dibuka 1 Desember 2026 pukul 10.00 WIB.",
			ImageURL:    commons + "7/71/GBK_Main_Stadium_during_Athletics_APG2018_%285%29.jpg/1280px-GBK_Main_Stadium_during_Athletics_APG2018_%285%29.jpg",
			ImageCredit: "Emille Ilmansyah, CC BY-SA 2.0, Wikimedia Commons: GBK Main Stadium during Athletics APG2018 (5).jpg",
			SaleStarts:  utc(2026, 12, 1, 3, 0), State: StateScheduled,
			Categories: []Category{
				{ID: "cat_garuda_vip", Name: "VIP", Description: "Tribun VIP dengan akses lounge.", Price: 2_500_000, Capacity: 1000, Available: 1000, MaxPerOrder: 3},
				{ID: "cat_garuda_cat1", Name: "CAT 1", Description: "Tribun bawah, pandangan terbaik.", Price: 1_250_000, Capacity: 8000, Available: 8000, MaxPerOrder: 3},
				{ID: "cat_garuda_cat2", Name: "CAT 2", Description: "Tribun atas.", Price: 650_000, Capacity: 20000, Available: 20000, MaxPerOrder: 3},
			},
		},
		{
			ID: "evt_jkt_tim_wayang", Name: "Malam Wayang Kontemporer", Category: "THEATER",
			Venue: "Teater Besar, Taman Ismail Marzuki", City: "Jakarta", Date: "2026-12-05", Time: "19:00",
			Description: "Pementasan wayang dengan tata panggung dan musik masa kini, di gedung teater yang baru direvitalisasi. Seluruh kursi sedang ditahan; kursi yang dilepas kembali dapat diambil lewat antrean.",
			ImageURL:    commons + "2/22/Tampak_Depan_Teater_Besar_Taman_Ismail_Marzuki_%28Januari_2025%29.jpg/1280px-Tampak_Depan_Teater_Besar_Taman_Ismail_Marzuki_%28Januari_2025%29.jpg",
			ImageCredit: "Medelam, CC BY-SA 4.0, Wikimedia Commons: Tampak Depan Teater Besar Taman Ismail Marzuki (Januari 2025).jpg",
			SaleStarts:  utc(2026, 11, 9, 3, 0), State: StateFullBooked,
			Categories: []Category{
				{ID: "cat_wayang_vip", Name: "VIP", Description: "Baris depan.", Price: 900_000, Capacity: 100, Available: 0, MaxPerOrder: 3},
				{ID: "cat_wayang_reguler", Name: "Reguler", Description: "Kursi balkon dan tengah.", Price: 350_000, Capacity: 900, Available: 0, MaxPerOrder: 3},
			},
		},
		{
			ID: "evt_jkt_ancol_sunset", Name: "Ancol Sunset Sessions", Category: "FESTIVAL",
			Venue: "Pantai Festival, Ancol", City: "Jakarta", Date: "2026-11-28", Time: "16:00",
			Description: "Musik akustik dan DJ di tepi pantai, dari sore sampai matahari terbenam di Teluk Jakarta. Bawa alas duduk; makanan dan minuman tersedia di lokasi.",
			ImageURL:    commons + "b/b0/Ancol_Beach_Jakarta_Sunset_RT.jpg/1280px-Ancol_Beach_Jakarta_Sunset_RT.jpg",
			ImageCredit: "Rantemario, CC BY-SA 4.0, Wikimedia Commons: Ancol Beach Jakarta Sunset RT.jpg",
			SaleStarts:  utc(2026, 11, 10, 3, 0), State: StateOpen,
			Categories: []Category{
				{ID: "cat_ancol_vip", Name: "VIP", Description: "Area teras dengan meja dan sofa.", Price: 600_000, Capacity: 200, Available: 200, MaxPerOrder: 3},
				{ID: "cat_ancol_festival", Name: "Festival", Description: "Area pantai umum.", Price: 275_000, Capacity: 2500, Available: 2500, MaxPerOrder: 3},
			},
		},
		{
			ID: "evt_jkt_jcc_creator_expo", Name: "Jakarta Creator Expo", Category: "EXPO",
			Venue: "Jakarta Convention Center, Senayan", City: "Jakarta", Date: "2026-10-24", Time: "10:00",
			Description: "Pameran satu hari untuk kreator konten, desainer, dan pelaku industri kreatif: talk, workshop, dan area demo. Penjualan sudah ditutup.",
			ImageURL:    commons + "8/8d/Jakarta_Convention_Center_%282024%29.jpg/1280px-Jakarta_Convention_Center_%282024%29.jpg",
			ImageCredit: "Medelam, CC BY-SA 4.0, Wikimedia Commons: Jakarta Convention Center (2024).jpg",
			SaleStarts:  utc(2026, 9, 1, 3, 0), State: StateClosed,
			Categories: []Category{
				{ID: "cat_expo_oneday", Name: "Tiket Sehari", Description: "Akses semua area dan sesi.", Price: 125_000, Capacity: 5000, Available: 0, MaxPerOrder: 3},
			},
		},
	}
}
