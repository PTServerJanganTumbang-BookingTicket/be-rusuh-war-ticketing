package entity

import "time"

// Message is a rendered email, ready to hand to the provider.
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}

// SentTTL is how long a delivered message is remembered for deduplication.
//
// Long enough to cover any plausible SQS redelivery, which happens within the
// visibility timeout and the retry policy — hours, not days. A week gives
// generous margin while keeping the key space bounded.
const SentTTL = 7 * 24 * time.Hour
