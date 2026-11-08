package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/testutil"
)

// nveMarket knows SIM-NVE (Nordvik Energy) and SIM-FJRD; every other id is unknown.
func nveMarket(prices map[string]float64) *testutil.MarketProvider {
	names := map[string][2]string{"SIM-NVE": {"NVE", "Nordvik Energy"}, "SIM-FJRD": {"FJRD", "Fjord Robotics"}}
	return &testutil.MarketProvider{
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			n, ok := names[id]
			if !ok {
				return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
			}
			return domain.InstrumentDetails{OrderbookID: id, Symbol: n[0], Name: n[1], LastPrice: prices[id], ChangePercent: 1.25}, nil
		},
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			if _, ok := names[id]; !ok {
				return domain.Quote{}, domain.ErrInstrumentNotFound
			}
			return domain.Quote{OrderbookID: id, Last: prices[id], ChangePercent: -0.42}, nil
		},
	}
}

func TestWatchlistService_CreateValidatesName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantErr  string
	}{
		{name: "plain", input: "Energy", wantName: "Energy"},
		{name: "trimmed", input: "  Energy \t", wantName: "Energy"},
		{name: "exactly 50 characters", input: strings.Repeat("a", 50), wantName: strings.Repeat("a", 50)},
		{name: "50 multi-byte characters", input: strings.Repeat("ö", 50), wantName: strings.Repeat("ö", 50)},
		{name: "empty", input: "", wantErr: "name is required"},
		{name: "blank", input: "   ", wantErr: "name is required"},
		{name: "51 characters", input: strings.Repeat("a", 51), wantErr: "name must be at most 50 characters"},
		{name: "surrounding spaces do not count", input: " " + strings.Repeat("a", 50) + " ", wantName: strings.Repeat("a", 50)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewWatchlistService(testutil.NewWatchlistRepo(), &testutil.MarketProvider{})
			wl, err := svc.Create(context.Background(), 1, domain.WatchlistNameRequest{Name: tt.input})
			if tt.wantErr != "" {
				var v *domain.ValidationError
				if !errors.As(err, &v) || v.Msg != tt.wantErr {
					t.Fatalf("error = %v, want ValidationError %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if wl.Name != tt.wantName || wl.ID == 0 || len(wl.Items) != 0 || wl.Items == nil {
				t.Errorf("Create() = %+v, want name %q, an id and empty items", wl, tt.wantName)
			}
		})
	}
}

func TestWatchlistService_CreateEnforcesLimitPerUser(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, &testutil.MarketProvider{})
	ctx := context.Background()
	for i := 0; i < domain.MaxWatchlists; i++ {
		if _, err := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "List"}); err != nil {
			t.Fatalf("list %d: %v", i+1, err)
		}
	}
	if _, err := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Eleventh"}); !errors.Is(err, domain.ErrWatchlistLimit) {
		t.Fatalf("11th list error = %v, want ErrWatchlistLimit", err)
	}
	// The limit is per user.
	if _, err := svc.Create(ctx, 2, domain.WatchlistNameRequest{Name: "Other user"}); err != nil {
		t.Fatalf("another user's first list: %v", err)
	}
}

func TestWatchlistService_CreatePassesMaxToRepository(t *testing.T) {
	var gotMax int
	repo := testutil.NewWatchlistRepo()
	repo.CreateFn = func(_ context.Context, userID uint, name string, max int) (*domain.Watchlist, error) {
		gotMax = max
		return &domain.Watchlist{ID: 1, Name: name}, nil
	}
	if _, err := NewWatchlistService(repo, &testutil.MarketProvider{}).Create(context.Background(), 1, domain.WatchlistNameRequest{Name: "X"}); err != nil {
		t.Fatal(err)
	}
	if gotMax != domain.MaxWatchlists {
		t.Errorf("max = %d, want %d", gotMax, domain.MaxWatchlists)
	}
}

func TestWatchlistService_AddItem(t *testing.T) {
	tests := []struct {
		name        string
		owner       uint // user that owns list 1
		caller      uint
		orderbookID string
		repeat      bool
		wantErr     error
		wantValid   bool
	}{
		{name: "adds SIM-NVE", owner: 1, caller: 1, orderbookID: "SIM-NVE"},
		{name: "trims the id", owner: 1, caller: 1, orderbookID: "  SIM-NVE "},
		{name: "blank id", owner: 1, caller: 1, orderbookID: " ", wantValid: true},
		{name: "duplicate", owner: 1, caller: 1, orderbookID: "SIM-NVE", repeat: true, wantErr: domain.ErrDuplicateItem},
		{name: "unknown instrument", owner: 1, caller: 1, orderbookID: "SIM-NOPE", wantErr: domain.ErrInstrumentNotFound},
		{name: "another user's list", owner: 1, caller: 2, orderbookID: "SIM-NVE", wantErr: domain.ErrWatchlistNotFound},
		{name: "another user's list hides an unknown instrument", owner: 1, caller: 2, orderbookID: "SIM-NOPE", wantErr: domain.ErrWatchlistNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo := testutil.NewWatchlistRepo()
			svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100}))
			wl, err := svc.Create(ctx, tt.owner, domain.WatchlistNameRequest{Name: "Energy"})
			if err != nil {
				t.Fatal(err)
			}
			req := domain.WatchlistItemRequest{OrderbookID: tt.orderbookID}
			if tt.repeat {
				if _, err := svc.AddItem(ctx, tt.caller, wl.ID, req); err != nil {
					t.Fatalf("first add: %v", err)
				}
			}
			item, err := svc.AddItem(ctx, tt.caller, wl.ID, req)
			switch {
			case tt.wantValid:
				var v *domain.ValidationError
				if !errors.As(err, &v) {
					t.Fatalf("error = %v, want ValidationError", err)
				}
				return
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			case err != nil:
				t.Fatalf("AddItem() error = %v", err)
			}
			if item.OrderbookID != "SIM-NVE" || item.Symbol != "NVE" || item.Name != "Nordvik Energy" || item.AddedAt.IsZero() {
				t.Errorf("item = %+v", item)
			}
			if item.LastPrice == nil || item.LastPrice.StringFixed(2) != "100.00" || item.ChangePercent == nil || *item.ChangePercent != 1.25 {
				t.Errorf("item price = %v / %v, want 100.00 / 1.25", item.LastPrice, item.ChangePercent)
			}
			got, _ := repo.Get(ctx, tt.owner, wl.ID)
			if len(got.Items) != 1 {
				t.Errorf("list has %d items, want 1", len(got.Items))
			}
		})
	}
}

func TestWatchlistService_AddItemMarketDataFailureIsUnavailable(t *testing.T) {
	ctx := context.Background()
	provider := &testutil.MarketProvider{DetailsFn: func(context.Context, string) (domain.InstrumentDetails, error) {
		return domain.InstrumentDetails{}, errors.New("timeout")
	}}
	svc := NewWatchlistService(testutil.NewWatchlistRepo(), provider)
	wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
	if _, err := svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"}); !errors.Is(err, domain.ErrMarketDataUnavailable) {
		t.Fatalf("error = %v, want ErrMarketDataUnavailable", err)
	}
}

func TestWatchlistService_GetPricesItemsFromQuotes(t *testing.T) {
	ctx := context.Background()
	prices := map[string]float64{"SIM-NVE": 100, "SIM-FJRD": 142.37}
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, nveMarket(prices))
	wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
	for _, id := range []string{"SIM-NVE", "SIM-FJRD"} {
		if _, err := svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: id}); err != nil {
			t.Fatal(err)
		}
	}
	// The price changes after the add: the read must use the current quote.
	prices["SIM-NVE"] = 101.5

	got, err := svc.Get(ctx, 1, wl.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SIM-NVE": "101.50", "SIM-FJRD": "142.37"}
	if len(got.Items) != 2 || got.Items[0].OrderbookID != "SIM-NVE" || got.Items[1].OrderbookID != "SIM-FJRD" {
		t.Fatalf("items = %+v, want SIM-NVE then SIM-FJRD", got.Items)
	}
	for _, item := range got.Items {
		if item.LastPrice == nil || item.LastPrice.StringFixed(2) != want[item.OrderbookID] {
			t.Errorf("%s last_price = %v, want %s", item.OrderbookID, item.LastPrice, want[item.OrderbookID])
		}
		if item.ChangePercent == nil || *item.ChangePercent != -0.42 {
			t.Errorf("%s change_percent = %v, want -0.42", item.OrderbookID, item.ChangePercent)
		}
	}

	if _, err := svc.Get(ctx, 2, wl.ID); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("another user's Get error = %v, want ErrWatchlistNotFound", err)
	}
}

func TestWatchlistService_FailedQuoteLeavesPriceNull(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100, "SIM-FJRD": 50}))
	wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
	_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"})
	_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-FJRD"})

	svc.provider = &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		if id == "SIM-NVE" {
			return domain.Quote{}, domain.ErrMarketDataUnavailable
		}
		return domain.Quote{Last: 50}, nil
	}}
	got, err := svc.Get(ctx, 1, wl.ID)
	if err != nil {
		t.Fatalf("a failed quote must not fail the read: %v", err)
	}
	if got.Items[0].LastPrice != nil || got.Items[0].ChangePercent != nil {
		t.Errorf("SIM-NVE price = %v / %v, want nil / nil", got.Items[0].LastPrice, got.Items[0].ChangePercent)
	}
	if got.Items[1].LastPrice == nil || got.Items[1].LastPrice.StringFixed(2) != "50.00" {
		t.Errorf("SIM-FJRD price = %v, want 50.00", got.Items[1].LastPrice)
	}
}

func TestWatchlistService_ListQuotesEachInstrumentOnceAndConcurrently(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100, "SIM-FJRD": 50}))
	for _, name := range []string{"A", "B"} {
		wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: name})
		_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"})
		_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-FJRD"})
	}

	var calls, inFlight, maxInFlight int32
	var mu sync.Mutex
	release := make(chan struct{})
	svc.provider = &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		atomic.AddInt32(&calls, 1)
		n := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if n > maxInFlight {
			maxInFlight = n
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		atomic.AddInt32(&inFlight, -1)
		return domain.Quote{Last: 10}, nil
	}}
	go func() {
		// Release once both distinct quotes are in flight, proving they run in parallel.
		deadline := time.Now().Add(time.Second)
		for atomic.LoadInt32(&inFlight) < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		close(release)
	}()

	res, err := svc.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("Quote called %d times, want 2 (once per distinct instrument)", calls)
	}
	if maxInFlight < 2 {
		t.Errorf("quotes ran sequentially (max in flight %d)", maxInFlight)
	}
	if len(res.Items) != 2 || res.NextCursor != nil {
		t.Fatalf("List() = %+v", res)
	}
	for _, wl := range res.Items {
		for _, item := range wl.Items {
			if item.LastPrice == nil || item.LastPrice.StringFixed(2) != "10.00" {
				t.Errorf("%s/%s price = %v", wl.Name, item.OrderbookID, item.LastPrice)
			}
		}
	}
}

func TestWatchlistService_ListEmptyIsNotNil(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	repo.ListFn = func(context.Context, uint) ([]domain.Watchlist, error) { return nil, nil }
	res, err := NewWatchlistService(repo, &testutil.MarketProvider{}).List(context.Background(), 1)
	if err != nil || res.Items == nil || len(res.Items) != 0 {
		t.Fatalf("List() = %+v, %v; want empty non-nil items", res, err)
	}
}

func TestWatchlistService_RenameDeleteRemoveAreOwnerScoped(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(svc *WatchlistService, userID, id uint) error
	}{
		{name: "rename", call: func(svc *WatchlistService, userID, id uint) error {
			_, err := svc.Rename(ctx, userID, id, domain.WatchlistNameRequest{Name: "Renewables"})
			return err
		}},
		{name: "remove item", call: func(svc *WatchlistService, userID, id uint) error {
			return svc.RemoveItem(ctx, userID, id, "SIM-NVE")
		}},
		{name: "delete", call: func(svc *WatchlistService, userID, id uint) error {
			return svc.Delete(ctx, userID, id)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testutil.NewWatchlistRepo()
			svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100}))
			wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
			_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"})

			if err := tt.call(svc, 2, wl.ID); !errors.Is(err, domain.ErrWatchlistNotFound) {
				t.Fatalf("other user: error = %v, want ErrWatchlistNotFound", err)
			}
			if err := tt.call(svc, 1, 999); !errors.Is(err, domain.ErrWatchlistNotFound) {
				t.Fatalf("missing list: error = %v, want ErrWatchlistNotFound", err)
			}
			got, err := repo.Get(ctx, 1, wl.ID)
			if err != nil || got.Name != "Energy" || len(got.Items) != 1 {
				t.Fatalf("owner's list changed by a failed call: %+v, %v", got, err)
			}
			if err := tt.call(svc, 1, wl.ID); err != nil {
				t.Fatalf("owner: %v", err)
			}
		})
	}
}

func TestWatchlistService_Rename(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100}))
	wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
	_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"})

	got, err := svc.Rename(ctx, 1, wl.ID, domain.WatchlistNameRequest{Name: " Renewables "})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renewables" || len(got.Items) != 1 || got.Items[0].LastPrice == nil {
		t.Errorf("Rename() = %+v, want the renamed, priced list", got)
	}
	var v *domain.ValidationError
	if _, err := svc.Rename(ctx, 1, wl.ID, domain.WatchlistNameRequest{Name: ""}); !errors.As(err, &v) {
		t.Errorf("blank rename error = %v, want ValidationError", err)
	}
}

func TestWatchlistService_RemoveItem(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewWatchlistRepo()
	svc := NewWatchlistService(repo, nveMarket(map[string]float64{"SIM-NVE": 100}))
	wl, _ := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"})
	_, _ = svc.AddItem(ctx, 1, wl.ID, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"})

	if err := svc.RemoveItem(ctx, 1, wl.ID, "SIM-NVE"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveItem(ctx, 1, wl.ID, "SIM-NVE"); !errors.Is(err, domain.ErrWatchlistItemNotFound) {
		t.Errorf("second remove error = %v, want ErrWatchlistItemNotFound", err)
	}
	var v *domain.ValidationError
	if err := svc.RemoveItem(ctx, 1, wl.ID, "  "); !errors.As(err, &v) {
		t.Errorf("blank id error = %v, want ValidationError", err)
	}
}
