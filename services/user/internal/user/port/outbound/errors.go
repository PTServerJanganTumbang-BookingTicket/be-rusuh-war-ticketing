package outbound

import "errors"

// ErrRefreshTokenReplayed is returned when a refresh token that was already
// redeemed is presented again.
//
// A sentinel rather than a generic failure because the use case must respond
// differently: a replay is evidence that the credential leaked, so the whole
// rotation chain is revoked and the customer is made to log in again. An
// expired or unknown token is just an expired or unknown token.
var ErrRefreshTokenReplayed = errors.New("refresh token already redeemed")
