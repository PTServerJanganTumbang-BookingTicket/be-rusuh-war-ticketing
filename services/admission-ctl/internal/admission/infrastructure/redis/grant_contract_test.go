package redis

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

// The grant hash has one writer (admission-ctl) and one reader (booking's grantauth). This test runs both against
// the real Redis cluster, so a renamed field on either side fails here instead of locking customers out.
func TestAGrantWrittenByTheControllerIsAcceptedByGrantauth(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set REDIS_ADDR (make verify does)")
	}
	rdb := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
	t.Cleanup(func() { _ = rdb.Close() })
	pepper := []byte("contract-pepper")
	const device = "contract-device-0001"
	suffix := fmt.Sprint(time.Now().UnixNano())

	// §8.17: an account grant is only honoured alongside that account's own
	// session, so the contract this test pins now covers both credentials.
	sessions, err := token.NewIssuer("contract-jwt-secret", "rusuh-user", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		info    domain.TokenInfo
		headers map[string]string
		want    grantauth.Grant
	}{
		{
			name:    "guest",
			info:    domain.TokenInfo{DeviceHash: grantauth.DeviceHash(pepper, device)},
			headers: map[string]string{grantauth.HeaderDeviceID: device},
			want:    grantauth.Grant{DeviceHash: grantauth.DeviceHash(pepper, device)},
		},
		{
			name: "logged-in customer",
			info: domain.TokenInfo{AccountID: "acc_contract"},
			want: grantauth.Grant{AccountID: "acc_contract"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grantToken := "gt_contract_" + tc.name[:5] + suffix
			b := domain.Booking{ID: "RB-" + suffix, GrantToken: grantToken}
			t.Cleanup(func() { rdb.Del(ctx, rediskeys.Grant(grantToken)) })
			if err := NewQueue(rdb).PutGrant(ctx, "evt_contract", "qt_"+suffix, b, tc.info, time.Minute); err != nil {
				t.Fatal(err)
			}

			var got *grantauth.Grant
			h := grantauth.Middleware(rdb, grantauth.Config{DevicePepper: pepper, Sessions: sessions})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if g, ok := grantauth.FromContext(r.Context()); ok {
					got = &g
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/bookings/"+b.ID, nil)
			req.Header.Set(grantauth.HeaderGrantToken, grantToken)
			if tc.info.AccountID != "" {
				raw, _, err := sessions.Issue(tc.info.AccountID, tc.info.AccountID+"@example.com", true)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(grantauth.HeaderAuthorization, "Bearer "+raw)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			want := tc.want
			want.Token, want.EventID, want.BookingID, want.QueueToken = grantToken, "evt_contract", b.ID, "qt_"+suffix
			if rec.Code != http.StatusNoContent || got == nil || *got != want {
				t.Fatalf("status %d, grant %+v; want 204 with %+v", rec.Code, got, want)
			}
		})
	}
}
