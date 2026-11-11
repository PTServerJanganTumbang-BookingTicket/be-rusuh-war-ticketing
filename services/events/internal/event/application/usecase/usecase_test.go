package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

var now = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)

type fakeEvents struct {
	events              []domain.Event
	cats                map[string][]domain.Category
	err                 error
	gets                int
	gotLimit, gotOffset int
}

func (f *fakeEvents) List(_ context.Context, limit, offset int) ([]domain.Event, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.events[min(offset, len(f.events)):min(offset+limit, len(f.events))], len(f.events), nil
}

func (f *fakeEvents) Get(_ context.Context, id string) (domain.Event, []domain.Category, bool, error) {
	f.gets++
	if f.err != nil {
		return domain.Event{}, nil, false, f.err
	}
	for _, e := range f.events {
		if e.ID == id {
			return e, f.cats[id], true, nil
		}
	}
	return domain.Event{}, nil, false, nil
}

type fakeLive struct {
	queues             map[string]domain.Queue
	stock              map[string]int64
	queueErr, stockErr error
}

func (f *fakeLive) Queues(context.Context, []string) (map[string]domain.Queue, error) {
	return f.queues, f.queueErr
}

func (f *fakeLive) Stock(context.Context, string, []string) (map[string]int64, error) {
	return f.stock, f.stockErr
}

type counter map[string]int

func (c counter) Incr(name string) { c[name]++ }

func start(h int) *time.Time { t := now.Add(time.Duration(h) * time.Hour); return &t }

func newDeps(ev *fakeEvents, live *fakeLive) (usecase.Deps, counter) {
	m := counter{}
	return usecase.Deps{Events: ev, Live: live, Metrics: m, Clock: func() time.Time { return now }}, m
}

var cfg = usecase.Config{QueueCapacity: 100, CaptchaSiteKey: "site-key"}

func threeEvents() *fakeEvents {
	return &fakeEvents{events: []domain.Event{
		{ID: "e-open", SaleStartsAt: start(-1)},
		{ID: "e-soon", SaleStartsAt: start(5)},
		{ID: "e-closed", SaleStartsAt: start(-9)},
	}}
}

func threeQueues() *fakeLive {
	return &fakeLive{queues: map[string]domain.Queue{
		"e-open":   {State: "OPEN", Depth: 3},
		"e-soon":   {State: "SCHEDULED"},
		"e-closed": {State: "CLOSED"},
	}}
}

func TestList_asksAuroraForTheRequestedPageOnlyAndKeepsTheTotal(t *testing.T) {
	ev := threeEvents()
	d, _ := newDeps(ev, threeQueues())
	l := usecase.NewList(d, cfg)
	res, err := l.Do(context.Background(), usecase.ListInput{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ev.gotLimit != 2 || ev.gotOffset != 1 {
		t.Fatalf("Aurora was asked for limit %d offset %d", ev.gotLimit, ev.gotOffset)
	}
	if res.Total != 3 || len(res.Cards) != 2 || res.Cards[0].Event.ID != "e-soon" || res.Cards[0].QueueState != domain.StateScheduled ||
		res.Cards[1].QueueState != domain.StateClosed {
		t.Fatalf("page = %+v", res)
	}
	res, _ = l.Do(context.Background(), usecase.ListInput{Limit: 2, Offset: 5})
	if res.Total != 3 || len(res.Cards) != 0 {
		t.Fatalf("an offset past the end must be an empty page, got %+v", res)
	}
}

func TestList_whenRedisFailsStatesComeFromSaleStartsAtAndItIsCounted(t *testing.T) {
	live := threeQueues()
	live.queueErr = errors.New("redis down")
	d, m := newDeps(threeEvents(), live)
	res, err := usecase.NewList(d, cfg).Do(context.Background(), usecase.ListInput{Limit: 10})
	if err != nil || !res.Degraded || m[domain.MetricRedisFallback] != 1 {
		t.Fatalf("err=%v degraded=%v metrics=%v", err, res.Degraded, m)
	}
	want := []domain.QueueState{domain.StateOpen, domain.StateScheduled, domain.StateOpen}
	for i, c := range res.Cards {
		if c.QueueState != want[i] {
			t.Errorf("%s: %s, want %s", c.Event.ID, c.QueueState, want[i])
		}
	}
}

func TestList_anEventWithoutAHashIsScheduledAndAnUnknownStateIsCounted(t *testing.T) {
	live := &fakeLive{queues: map[string]domain.Queue{"e-open": {State: "PAUSED"}}}
	d, m := newDeps(threeEvents(), live)
	res, _ := usecase.NewList(d, cfg).Do(context.Background(), usecase.ListInput{Limit: 10})
	for _, c := range res.Cards {
		if c.QueueState != domain.StateScheduled {
			t.Errorf("%s: %s", c.Event.ID, c.QueueState)
		}
	}
	if m[domain.MetricUnknownState] != 1 {
		t.Fatalf("metrics = %v", m)
	}
}

func TestList_whenAuroraFailsItIs503(t *testing.T) {
	ev := threeEvents()
	ev.err = errors.New("boom")
	d, _ := newDeps(ev, threeQueues())
	_, err := usecase.NewList(d, cfg).Do(context.Background(), usecase.ListInput{Limit: 10})
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != 503 || ae.ErrorCode != "INTERNAL_ERROR" {
		t.Fatalf("err = %v", err)
	}
}

func detailEvents() *fakeEvents {
	return &fakeEvents{
		events: []domain.Event{{ID: "e1", Name: "Konser", SaleStartsAt: start(-1)}},
		cats: map[string][]domain.Category{"e1": {
			{ID: "vip", Price: 1500000, Available: 12},
			{ID: "fest", Price: 850000, Available: 240},
			{ID: "trib", Price: 500000, Available: 7},
		}},
	}
}

func TestGet_usesLiveStockAndMarksACategoryWithNoSeatsSoldOut(t *testing.T) {
	live := &fakeLive{queues: map[string]domain.Queue{"e1": {State: "OPEN"}}, stock: map[string]int64{"vip": 5, "fest": 0, "trib": -3}}
	d, _ := newDeps(detailEvents(), live)
	got, err := usecase.NewGet(d, cfg).Do(context.Background(), "e1")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		avail  int64
		status string
	}{{5, "AVAILABLE"}, {0, "SOLD_OUT"}, {0, "SOLD_OUT"}}
	for i, w := range want {
		if got.Categories[i].Available != w.avail || got.Categories[i].Status != w.status {
			t.Errorf("category %d = %+v, want %+v", i, got.Categories[i], w)
		}
	}
	if got.QueueState != domain.StateOpen || got.CaptchaSiteKey != "site-key" || got.Degraded {
		t.Fatalf("detail = %+v", got)
	}
}

func TestGet_aMissingStockKeyFallsBackToTheSnapshotForThatCategoryOnly(t *testing.T) {
	live := &fakeLive{queues: map[string]domain.Queue{"e1": {State: "OPEN"}}, stock: map[string]int64{"vip": 5}}
	d, _ := newDeps(detailEvents(), live)
	got, _ := usecase.NewGet(d, cfg).Do(context.Background(), "e1")
	if got.Categories[0].Available != 5 || got.Categories[1].Available != 240 || got.Categories[2].Available != 7 {
		t.Fatalf("categories = %+v", got.Categories)
	}
}

func TestGet_whenRedisFailsItUsesTheSnapshotAndIsDegraded(t *testing.T) {
	live := &fakeLive{stockErr: errors.New("redis down"), queueErr: errors.New("redis down")}
	d, m := newDeps(detailEvents(), live)
	got, err := usecase.NewGet(d, cfg).Do(context.Background(), "e1")
	if err != nil || !got.Degraded || m[domain.MetricRedisFallback] != 1 {
		t.Fatalf("err=%v degraded=%v metrics=%v", err, got.Degraded, m)
	}
	if got.Categories[0].Available != 12 || got.QueueState != domain.StateOpen {
		t.Fatalf("detail = %+v", got)
	}
}

func TestGet_anIdThatCannotBeARedisKeyIs404WithoutTouchingAurora(t *testing.T) {
	ev := detailEvents()
	d, _ := newDeps(ev, &fakeLive{})
	for _, id := range []string{"", "{x}", "a b"} {
		_, err := usecase.NewGet(d, cfg).Do(context.Background(), id)
		var ae *apperror.AppError
		if !errors.As(err, &ae) || ae.HTTPStatus != 404 || ae.ErrorCode != domain.CodeNotFound {
			t.Fatalf("%q: err = %v", id, err)
		}
	}
	if ev.gets != 0 {
		t.Fatal("Aurora was read for an invalid id")
	}
}

func TestGet_anUnknownEventIs404AndAuroraFailureIs503(t *testing.T) {
	d, _ := newDeps(detailEvents(), &fakeLive{})
	_, err := usecase.NewGet(d, cfg).Do(context.Background(), "nope")
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.HTTPStatus != 404 {
		t.Fatalf("err = %v", err)
	}
	ev := detailEvents()
	ev.err = errors.New("boom")
	d, _ = newDeps(ev, &fakeLive{})
	_, err = usecase.NewGet(d, cfg).Do(context.Background(), "e1")
	if !errors.As(err, &ae) || ae.HTTPStatus != 503 {
		t.Fatalf("err = %v", err)
	}
}
