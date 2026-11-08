package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

var snapAt = time.Date(2026, 11, 8, 9, 41, 0, 0, time.FixedZone("CET", 3600))

func snapFixedNow() time.Time { return snapAt }

func TestSnapshotService_TakeSnapshot(t *testing.T) {
	tests := []struct {
		name                           string
		cash                           string
		positions                      []models.Position
		quotes                         map[string]quoteResult
		wantCash, wantHoldings, wantEq string
	}{
		{
			// AC2 of NW-10: 100 SIM-FJRD bought for 10,035.03 and valued at last 100.
			name: "holdings at the last price", cash: "89964.97",
			positions: []models.Position{holding("FJRD", "100", "100.3503")},
			quotes:    map[string]quoteResult{"SIM-FJRD": last(100, 0)},
			wantCash:  "89964.97", wantHoldings: "10000", wantEq: "99964.97",
		},
		{
			name: "no positions is all cash", cash: "100000",
			wantCash: "100000", wantHoldings: "0", wantEq: "100000",
		},
		{
			name: "a failed quote values the position at avg_cost", cash: "1000",
			positions: []models.Position{holding("NVE", "10", "100.35"), holding("FJRD", "2", "50")},
			quotes:    map[string]quoteResult{"SIM-NVE": {err: domain.ErrMarketDataUnavailable}, "SIM-FJRD": last(55.555, 0)},
			// 10 × 100.35 + 2 × 55.56 (last rounded to 2 decimals first, as in GET /portfolio)
			wantCash: "1000", wantHoldings: "1114.62", wantEq: "2114.62",
		},
		{
			name: "a zero last price values at avg_cost", cash: "0",
			positions: []models.Position{holding("NVE", "1.5", "10")},
			quotes:    map[string]quoteResult{"SIM-NVE": last(0, 0)},
			wantCash:  "0", wantHoldings: "15", wantEq: "15",
		},
		{
			// 0.3333 × 10.01 = 3.336333 → holdings 3.34; equity = round2(0.004 + 3.336333) = 3.34
			name: "rounding matches GET /portfolio", cash: "0.004",
			positions: []models.Position{holding("NVE", "0.3333", "10")},
			quotes:    map[string]quoteResult{"SIM-NVE": last(10.01, 0)},
			wantCash:  "0", wantHoldings: "3.34", wantEq: "3.34",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.SnapshotRepo{}
			users := testutil.NewUserRepo(&models.User{ID: 7, Balance: dec(tt.cash)})
			svc := NewSnapshotService(repo, &testutil.PortfolioRepo{Positions: tt.positions}, users, marketOf(tt.quotes), snapFixedNow)

			if err := svc.TakeSnapshot(context.Background(), 7); err != nil {
				t.Fatalf("TakeSnapshot() error = %v", err)
			}
			got := repo.ForUser(7)
			if len(got) != 1 {
				t.Fatalf("stored %d snapshots, want 1", len(got))
			}
			s := got[0]
			if !s.T.Equal(snapAt) || s.T.Location() != time.UTC {
				t.Errorf("T = %v, want %v in UTC", s.T, snapAt)
			}
			if !s.Cash.Equal(dec(tt.wantCash)) || !s.HoldingsValue.Equal(dec(tt.wantHoldings)) ||
				!s.Equity.Equal(dec(tt.wantEq)) || !s.VaultValue.IsZero() {
				t.Errorf("snapshot = cash %s holdings %s vault %s equity %s; want %s %s 0 %s",
					s.Cash, s.HoldingsValue, s.VaultValue, s.Equity, tt.wantCash, tt.wantHoldings, tt.wantEq)
			}
		})
	}
}

// The snapshot after a fill must equal GET /portfolio's total_equity (AC3 of NW-10).
func TestSnapshotService_MatchesPortfolioTotalEquity(t *testing.T) {
	positions := []models.Position{holding("FJRD", "100", "100.3503"), holding("NVE", "3.3333", "99.99")}
	market := marketOf(map[string]quoteResult{"SIM-FJRD": last(100.004, 1), "SIM-NVE": last(101.236, -2)})
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec("89633.33")})
	positionsRepo := &testutil.PortfolioRepo{Positions: positions}

	p, err := NewPortfolioService(positionsRepo, market, users).GetPortfolio(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	repo := &testutil.SnapshotRepo{}
	if err := NewSnapshotService(repo, positionsRepo, users, market, snapFixedNow).TakeSnapshot(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := domain.MoneyFloat(repo.Snapshots[0].Equity); got != p.TotalEquity {
		t.Errorf("snapshot equity %v != portfolio total_equity %v", got, p.TotalEquity)
	}
}

func TestSnapshotService_TakeSnapshotErrors(t *testing.T) {
	dbDown := errors.New("db down")
	tests := []struct {
		name      string
		users     *testutil.UserRepo
		positions *testutil.PortfolioRepo
		repo      *testutil.SnapshotRepo
		want      error
	}{
		{"unknown user", testutil.NewUserRepo(), &testutil.PortfolioRepo{}, &testutil.SnapshotRepo{}, domain.ErrUserNotFound},
		{"positions fail", testutil.NewUserRepo(&models.User{ID: 1}), &testutil.PortfolioRepo{Err: dbDown}, &testutil.SnapshotRepo{}, dbDown},
		{"insert fails", testutil.NewUserRepo(&models.User{ID: 1}), &testutil.PortfolioRepo{},
			&testutil.SnapshotRepo{InsertBatchFn: func(context.Context, []models.EquitySnapshot) error { return dbDown }}, dbDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewSnapshotService(tt.repo, tt.positions, tt.users, marketOf(nil), snapFixedNow).TakeSnapshot(context.Background(), 1)
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSnapshotService_OrderFilled(t *testing.T) {
	t.Run("snapshots with a detached context", func(t *testing.T) {
		var sawErr error
		repo := &testutil.SnapshotRepo{}
		users := testutil.NewUserRepo(&models.User{ID: 3, Balance: dec("100")})
		users.GetByIDFn = func(ctx context.Context, id uint) (*models.User, error) {
			sawErr = ctx.Err()
			return &models.User{ID: id, Balance: dec("100")}, nil
		}
		svc := NewSnapshotService(repo, &testutil.PortfolioRepo{}, users, marketOf(nil), snapFixedNow)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the client hung up right after its order
		svc.OrderFilled(ctx, 3)

		if sawErr != nil {
			t.Errorf("the snapshot ran with a cancelled context: %v", sawErr)
		}
		if got := repo.ForUser(3); len(got) != 1 || !got[0].Equity.Equal(dec("100")) {
			t.Errorf("snapshots = %+v, want one at 100", got)
		}
	})

	t.Run("a failure is swallowed", func(t *testing.T) {
		svc := NewSnapshotService(&testutil.SnapshotRepo{}, &testutil.PortfolioRepo{}, testutil.NewUserRepo(), marketOf(nil), snapFixedNow)
		svc.OrderFilled(context.Background(), 99) // must not panic; the fill already happened
	})
}

func TestSnapshotService_SnapshotAll(t *testing.T) {
	var fjrdQuotes atomic.Int32
	market := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		switch id {
		case "SIM-FJRD":
			fjrdQuotes.Add(1)
			return domain.Quote{Last: 100}, nil
		case "SIM-NVE":
			return domain.Quote{Last: 110}, nil
		}
		return domain.Quote{}, domain.ErrInstrumentNotFound
	}}
	users := testutil.NewUserRepo()
	byUser := map[uint][]models.Position{}
	var ids []uint
	for i := uint(1); i <= 20; i++ {
		users.Users[i] = &models.User{ID: i, Balance: dec("1000")}
		byUser[i] = []models.Position{holding("FJRD", "1", "90")}
		ids = append(ids, i)
	}
	byUser[2] = append(byUser[2], holding("NVE", "2", "100"))
	ids = append(ids, 404) // has orders but cannot be loaded: skipped, not fatal
	repo := &testutil.SnapshotRepo{UsersWithOrders: ids}
	svc := NewSnapshotService(repo, &testutil.PortfolioRepo{ByUser: byUser}, users, market, snapFixedNow)

	n, err := svc.SnapshotAll(context.Background())
	if err != nil {
		t.Fatalf("SnapshotAll() error = %v", err)
	}
	if n != 20 || len(repo.Snapshots) != 20 {
		t.Fatalf("wrote %d (stored %d), want 20", n, len(repo.Snapshots))
	}
	if len(repo.Batches) != 1 || repo.Batches[0] != 20 {
		t.Errorf("batches = %v, want one batch of 20", repo.Batches)
	}
	if got := fjrdQuotes.Load(); got != 1 {
		t.Errorf("SIM-FJRD quoted %d times, want once per run", got)
	}
	for _, s := range repo.Snapshots {
		if !s.T.Equal(snapAt) {
			t.Errorf("user %d at %v, want the shared instant %v", s.UserID, s.T, snapAt)
		}
		want := "1100"
		if s.UserID == 2 {
			want = "1320"
		}
		if !s.Equity.Equal(dec(want)) {
			t.Errorf("user %d equity %s, want %s", s.UserID, s.Equity, want)
		}
	}
}

func TestSnapshotService_SnapshotAllEdges(t *testing.T) {
	dbDown := errors.New("db down")
	t.Run("nobody has orders", func(t *testing.T) {
		repo := &testutil.SnapshotRepo{}
		n, err := NewSnapshotService(repo, &testutil.PortfolioRepo{}, testutil.NewUserRepo(), marketOf(nil), snapFixedNow).SnapshotAll(context.Background())
		if err != nil || n != 0 || len(repo.Batches) != 0 {
			t.Errorf("= %d, %v, batches %v; want 0, nil, none", n, err, repo.Batches)
		}
	})
	t.Run("listing users fails", func(t *testing.T) {
		repo := &testutil.SnapshotRepo{UserIDsWithOrdersFn: func(context.Context) ([]uint, error) { return nil, dbDown }}
		if _, err := NewSnapshotService(repo, &testutil.PortfolioRepo{}, testutil.NewUserRepo(), marketOf(nil), snapFixedNow).SnapshotAll(context.Background()); !errors.Is(err, dbDown) {
			t.Errorf("error = %v, want %v", err, dbDown)
		}
	})
	t.Run("insert fails", func(t *testing.T) {
		repo := &testutil.SnapshotRepo{UsersWithOrders: []uint{1},
			InsertBatchFn: func(context.Context, []models.EquitySnapshot) error { return dbDown }}
		users := testutil.NewUserRepo(&models.User{ID: 1})
		if _, err := NewSnapshotService(repo, &testutil.PortfolioRepo{}, users, marketOf(nil), snapFixedNow).SnapshotAll(context.Background()); !errors.Is(err, dbDown) {
			t.Errorf("error = %v, want %v", err, dbDown)
		}
	})
}

func TestSnapshotService_CachedQuotesConcurrent(t *testing.T) {
	var calls atomic.Int32
	market := &testutil.MarketProvider{QuoteFn: func(context.Context, string) (domain.Quote, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return domain.Quote{Last: 5}, nil
	}}
	q := NewSnapshotService(nil, nil, nil, market, nil).cachedQuotes()
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := q(context.Background(), "SIM-X"); !got.ok || !got.last.Equal(dec("5")) {
				t.Errorf("quote = %+v", got)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("quoted %d times, want 1", calls.Load())
	}
}
