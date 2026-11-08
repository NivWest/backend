package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

// holding is an open position in the instrument orderbookID ("SIM-" + symbol) at the given cost basis.
func holding(symbol, qty, avgCost string) models.Position {
	return models.Position{
		Stock:    models.Stock{OrderbookID: "SIM-" + symbol, Symbol: symbol, Name: symbol + " AB"},
		Quantity: dec(qty),
		AvgCost:  dec(avgCost),
	}
}

// quoteResult is what the fake market returns for one orderbook id.
type quoteResult struct {
	quote domain.Quote
	err   error
}

// marketOf returns a fake market answering from quotes; unknown ids fail with ErrInstrumentNotFound.
func marketOf(quotes map[string]quoteResult) *testutil.MarketProvider {
	return &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		r, ok := quotes[id]
		if !ok {
			return domain.Quote{}, domain.ErrInstrumentNotFound
		}
		return r.quote, r.err
	}}
}

func last(price, change float64) quoteResult {
	return quoteResult{quote: domain.Quote{Last: price, ChangePercent: change}}
}

func getPortfolio(t *testing.T, cash, realized string, positions []models.Position, market *testutil.MarketProvider) *domain.Portfolio {
	t.Helper()
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec(cash), RealizedPnL: dec(realized)})
	p, err := NewPortfolioService(&testutil.PortfolioRepo{Positions: positions}, market, users).
		GetPortfolio(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetPortfolio() error = %v", err)
	}
	if p.Positions == nil || p.TopMovers == nil {
		t.Fatalf("Positions/TopMovers must never be nil: %+v", p)
	}
	return p
}

func TestPortfolioService_GetPortfolio_Values(t *testing.T) {
	nve := domain.PortfolioPosition{
		OrderbookID: "SIM-NVE", Symbol: "NVE", Name: "NVE AB", Quantity: 10, AvgCost: 100.35, LastPrice: 110,
		MarketValue: 1100, UnrealizedPnL: 96.50, UnrealizedPnLPercent: 9.62, DayChangePercent: 1.25, Weight: 100,
	}
	nveHalf := nve
	nveHalf.Quantity, nveHalf.MarketValue, nveHalf.UnrealizedPnL = 5, 550, 48.25

	aaa := domain.PortfolioPosition{OrderbookID: "SIM-AAA", Symbol: "AAA", Name: "AAA AB", Quantity: 10, AvgCost: 100,
		LastPrice: 150, MarketValue: 1500, UnrealizedPnL: 500, UnrealizedPnLPercent: 50, DayChangePercent: 2.5, Weight: 45.45}
	bbb := domain.PortfolioPosition{OrderbookID: "SIM-BBB", Symbol: "BBB", Name: "BBB AB", Quantity: 5, AvgCost: 100,
		LastPrice: 100, MarketValue: 500, DayChangePercent: -1, Weight: 15.15}
	ccc := domain.PortfolioPosition{OrderbookID: "SIM-CCC", Symbol: "CCC", Name: "CCC AB", Quantity: 20, AvgCost: 25,
		LastPrice: 25, MarketValue: 500, Weight: 15.15}
	ddd := domain.PortfolioPosition{OrderbookID: "SIM-DDD", Symbol: "DDD", Name: "DDD AB", Quantity: 4, AvgCost: 250,
		LastPrice: 200, MarketValue: 800, UnrealizedPnL: -200, UnrealizedPnLPercent: -20, DayChangePercent: -3, Weight: 24.24}

	errPos := domain.PortfolioPosition{OrderbookID: "SIM-ERR", Symbol: "ERR", Name: "ERR AB", Quantity: 2, AvgCost: 40.5,
		LastPrice: 40.5, MarketValue: 81, Weight: 38.57}
	gonePos := domain.PortfolioPosition{OrderbookID: "SIM-GONE", Symbol: "GONE", Name: "GONE AB", Quantity: 3, AvgCost: 10,
		LastPrice: 10, MarketValue: 30, Weight: 14.29}
	zeroPos := domain.PortfolioPosition{OrderbookID: "SIM-ZERO", Symbol: "ZERO", Name: "ZERO AB", Quantity: 1, AvgCost: 99,
		LastPrice: 99, MarketValue: 99, Weight: 47.14}

	tests := []struct {
		name      string
		cash      string
		realized  string
		positions []models.Position
		quotes    map[string]quoteResult
		want      domain.Portfolio
	}{
		{
			name:      "story NW-05 AC1: buy 10 SIM-NVE for 1003.50, last 110",
			cash:      "98996.50",
			realized:  "0",
			positions: []models.Position{holding("NVE", "10", "100.35")},
			quotes:    map[string]quoteResult{"SIM-NVE": last(110, 1.25)},
			want: domain.Portfolio{
				Cash: 98996.50, BuyingPower: 98996.50, HoldingsValue: 1100, TotalEquity: 100096.50, UnrealizedPnL: 96.50, RealizedPnL: 0,
				Positions: []domain.PortfolioPosition{nve}, TopMovers: []domain.PortfolioPosition{nve},
			},
		},
		{
			name:      "story NW-05 AC2: sold 5 for 548.08, avg_cost unchanged",
			cash:      "99544.58",
			realized:  "46.33",
			positions: []models.Position{holding("NVE", "5", "100.35")},
			quotes:    map[string]quoteResult{"SIM-NVE": last(110, 1.25)},
			want: domain.Portfolio{
				Cash: 99544.58, BuyingPower: 99544.58, HoldingsValue: 550, TotalEquity: 100094.58, UnrealizedPnL: 48.25, RealizedPnL: 46.33,
				Positions: []domain.PortfolioPosition{nveHalf}, TopMovers: []domain.PortfolioPosition{nveHalf},
			},
		},
		{
			name:     "empty portfolio is cash only with empty arrays",
			cash:     "100000",
			realized: "0",
			want: domain.Portfolio{
				Cash: 100000, BuyingPower: 100000, TotalEquity: 100000,
				Positions: []domain.PortfolioPosition{}, TopMovers: []domain.PortfolioPosition{},
			},
		},
		{
			name:     "several positions: weights, market value sort with orderbook id tie-break",
			cash:     "1000",
			realized: "-12.34",
			positions: []models.Position{
				holding("BBB", "5", "100"), holding("CCC", "20", "25"), holding("DDD", "4", "250"), holding("AAA", "10", "100"),
			},
			quotes: map[string]quoteResult{
				"SIM-AAA": last(150, 2.5), "SIM-BBB": last(100, -1), "SIM-CCC": last(25, 0), "SIM-DDD": last(200, -3),
			},
			want: domain.Portfolio{
				Cash: 1000, BuyingPower: 1000, HoldingsValue: 3300, TotalEquity: 4300, UnrealizedPnL: 300, RealizedPnL: -12.34,
				Positions: []domain.PortfolioPosition{aaa, ddd, bbb, ccc},
				TopMovers: []domain.PortfolioPosition{aaa, ccc, bbb, ddd},
			},
		},
		{
			name:     "quote failure, unknown instrument and zero price fall back to avg_cost with day change 0",
			cash:     "500",
			realized: "0",
			positions: []models.Position{
				holding("ERR", "2", "40.5"), holding("GONE", "3", "10"), holding("ZERO", "1", "99"),
			},
			quotes: map[string]quoteResult{
				"SIM-ERR":  {err: fmt.Errorf("%w: upstream down", domain.ErrMarketDataUnavailable)},
				"SIM-ZERO": last(0, 5),
			},
			want: domain.Portfolio{
				Cash: 500, BuyingPower: 500, HoldingsValue: 210, TotalEquity: 710,
				Positions: []domain.PortfolioPosition{zeroPos, errPos, gonePos},
				TopMovers: []domain.PortfolioPosition{errPos, gonePos, zeroPos},
			},
		},
		{
			name:      "rounds half-up after decimal math: fractional quantity, day change 1.005",
			cash:      "98996.50",
			realized:  "0",
			positions: []models.Position{holding("FJRD", "0.3333", "100.10")},
			// market value 33.353331, cost 33.36333, unrealized -0.009999 (-0.02997%)
			quotes: map[string]quoteResult{"SIM-FJRD": last(100.07, 1.005)},
			want: func() domain.Portfolio {
				p := domain.PortfolioPosition{OrderbookID: "SIM-FJRD", Symbol: "FJRD", Name: "FJRD AB", Quantity: 0.3333,
					AvgCost: 100.10, LastPrice: 100.07, MarketValue: 33.35, UnrealizedPnL: -0.01,
					UnrealizedPnLPercent: -0.03, DayChangePercent: 1.01, Weight: 100}
				return domain.Portfolio{Cash: 98996.50, BuyingPower: 98996.50, HoldingsValue: 33.35, TotalEquity: 99029.85, UnrealizedPnL: -0.01,
					Positions: []domain.PortfolioPosition{p}, TopMovers: []domain.PortfolioPosition{p}}
			}(),
		},
		{
			name:      "rounds half-up: last 10.065, 8-decimal avg_cost, negative day change -0.125",
			cash:      "1000",
			realized:  "0.005",
			positions: []models.Position{holding("HALF", "3", "10.12345678")},
			// last 10.07, market value 30.21, cost 30.37037034, unrealized -0.16037034 (-0.52805%)
			quotes: map[string]quoteResult{"SIM-HALF": last(10.065, -0.125)},
			want: func() domain.Portfolio {
				p := domain.PortfolioPosition{OrderbookID: "SIM-HALF", Symbol: "HALF", Name: "HALF AB", Quantity: 3,
					AvgCost: 10.12, LastPrice: 10.07, MarketValue: 30.21, UnrealizedPnL: -0.16,
					UnrealizedPnLPercent: -0.53, DayChangePercent: -0.13, Weight: 100}
				return domain.Portfolio{Cash: 1000, BuyingPower: 1000, HoldingsValue: 30.21, TotalEquity: 1030.21, UnrealizedPnL: -0.16,
					RealizedPnL: 0.01, Positions: []domain.PortfolioPosition{p}, TopMovers: []domain.PortfolioPosition{p}}
			}(),
		},
		{
			name:      "rounds quantity to 4 decimals",
			cash:      "0",
			realized:  "0",
			positions: []models.Position{holding("QTY", "1.23456", "10")},
			quotes:    map[string]quoteResult{"SIM-QTY": last(10, 0)},
			want: func() domain.Portfolio {
				p := domain.PortfolioPosition{OrderbookID: "SIM-QTY", Symbol: "QTY", Name: "QTY AB", Quantity: 1.2346,
					AvgCost: 10, LastPrice: 10, MarketValue: 12.35, Weight: 100}
				return domain.Portfolio{HoldingsValue: 12.35, TotalEquity: 12.35,
					Positions: []domain.PortfolioPosition{p}, TopMovers: []domain.PortfolioPosition{p}}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getPortfolio(t, tt.cash, tt.realized, tt.positions, marketOf(tt.quotes))
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("GetPortfolio() =\n%+v\nwant\n%+v", *got, tt.want)
			}
		})
	}
}

func TestPortfolioService_GetPortfolio_TopMovers(t *testing.T) {
	tests := []struct {
		name          string
		changes       map[string]float64 // symbol -> day change percent; every position is 1 share at 10
		wantPositions []string
		wantMovers    []string
	}{
		{
			name:          "capped at MaxTopMovers, day change desc, ties by orderbook id",
			changes:       map[string]float64{"S1": 1, "S2": 7, "S3": 3, "S4": 7, "S5": -2, "S6": 5, "S7": 0.5},
			wantPositions: []string{"SIM-S1", "SIM-S2", "SIM-S3", "SIM-S4", "SIM-S5", "SIM-S6", "SIM-S7"},
			wantMovers:    []string{"SIM-S2", "SIM-S4", "SIM-S6", "SIM-S3", "SIM-S1"},
		},
		{
			name:          "exactly MaxTopMovers positions are all movers",
			changes:       map[string]float64{"A": -1, "B": -5, "C": 2, "D": 0, "E": 2},
			wantPositions: []string{"SIM-A", "SIM-B", "SIM-C", "SIM-D", "SIM-E"},
			wantMovers:    []string{"SIM-C", "SIM-E", "SIM-D", "SIM-A", "SIM-B"},
		},
		{
			name:          "fewer positions than the cap, losers included",
			changes:       map[string]float64{"X": -4, "Y": -0.5},
			wantPositions: []string{"SIM-X", "SIM-Y"},
			wantMovers:    []string{"SIM-Y", "SIM-X"},
		},
		{
			name:          "changes equal after rounding to 2 decimals tie-break by orderbook id",
			changes:       map[string]float64{"M2": 1.004, "M1": 1.001},
			wantPositions: []string{"SIM-M1", "SIM-M2"},
			wantMovers:    []string{"SIM-M1", "SIM-M2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var positions []models.Position
			quotes := map[string]quoteResult{}
			for symbol, change := range tt.changes {
				positions = append(positions, holding(symbol, "1", "10"))
				quotes["SIM-"+symbol] = last(10, change)
			}
			got := getPortfolio(t, "0", "0", positions, marketOf(quotes))
			if ids := orderbookIDs(got.Positions); !reflect.DeepEqual(ids, tt.wantPositions) {
				t.Errorf("Positions = %v, want %v", ids, tt.wantPositions)
			}
			if ids := orderbookIDs(got.TopMovers); !reflect.DeepEqual(ids, tt.wantMovers) {
				t.Errorf("TopMovers = %v, want %v", ids, tt.wantMovers)
			}
			if len(got.TopMovers) > domain.MaxTopMovers {
				t.Errorf("len(TopMovers) = %d, want at most %d", len(got.TopMovers), domain.MaxTopMovers)
			}
		})
	}
}

func orderbookIDs(positions []domain.PortfolioPosition) []string {
	ids := make([]string, 0, len(positions))
	for _, p := range positions {
		ids = append(ids, p.OrderbookID)
	}
	return ids
}

func TestPortfolioService_GetPortfolio_PropagatesErrors(t *testing.T) {
	repoErr := errors.New("db down")
	tests := []struct {
		name    string
		users   *testutil.UserRepo
		repo    *testutil.PortfolioRepo
		wantErr error
	}{
		{name: "user not found", users: testutil.NewUserRepo(), repo: &testutil.PortfolioRepo{}, wantErr: domain.ErrUserNotFound},
		{
			name:    "positions query fails",
			users:   testutil.NewUserRepo(&models.User{ID: 1}),
			repo:    &testutil.PortfolioRepo{Err: repoErr},
			wantErr: repoErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPortfolioService(tt.repo, &testutil.MarketProvider{}, tt.users).GetPortfolio(context.Background(), 1)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Quotes are requested by orderbook id (never the symbol), once per position, and concurrently: every call
// waits until all of them are in flight, which only succeeds when they run in parallel.
func TestPortfolioService_GetPortfolio_QuotesConcurrentlyByOrderbookID(t *testing.T) {
	positions := []models.Position{holding("VOLV-B", "1", "1"), holding("ABB", "1", "1"), holding("NVE", "1", "1")}
	var (
		mu    sync.Mutex
		asked []string
	)
	allIn := make(chan struct{})
	market := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		mu.Lock()
		asked = append(asked, id)
		if len(asked) == len(positions) {
			close(allIn)
		}
		mu.Unlock()
		select {
		case <-allIn:
			return domain.Quote{Last: 2}, nil
		case <-time.After(2 * time.Second):
			return domain.Quote{}, errors.New("quotes were not fetched concurrently")
		}
	}}

	got := getPortfolio(t, "0", "0", positions, market)

	sort.Strings(asked)
	if want := []string{"SIM-ABB", "SIM-NVE", "SIM-VOLV-B"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("quoted %v, want %v (orderbook ids, once each)", asked, want)
	}
	for _, p := range got.Positions {
		if p.LastPrice != 2 {
			t.Errorf("%s LastPrice = %v, want the live quote 2", p.OrderbookID, p.LastPrice)
		}
	}
}

// Story NW-08: reservations of OPEN orders are part of cash, shown as reserved_cash, and held back from buying power;
// reserved shares are reported per position.
func TestPortfolioService_GetPortfolio_Reservations(t *testing.T) {
	tests := []struct {
		name                       string
		cash, reserved             string
		reservedQty                string
		wantReserved, wantBuyPower float64
		wantReservedQty            float64
	}{
		{name: "AC1 buy limit 10 @ 95 reserves 952.38", cash: "100000.00", reserved: "952.38", reservedQty: "0",
			wantReserved: 952.38, wantBuyPower: 99047.62},
		{name: "nothing reserved", cash: "500.00", reserved: "0", reservedQty: "0", wantReserved: 0, wantBuyPower: 500},
		{name: "all cash reserved and shares held back", cash: "1000.00", reserved: "1000.00", reservedQty: "2.5",
			wantReserved: 1000, wantBuyPower: 0, wantReservedQty: 2.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec(tt.cash), ReservedCash: dec(tt.reserved)})
			pos := holding("KALL", "10", "100")
			pos.ReservedQuantity = dec(tt.reservedQty)
			p, err := NewPortfolioService(&testutil.PortfolioRepo{Positions: []models.Position{pos}},
				marketOf(map[string]quoteResult{"SIM-KALL": last(100, 0)}), users).GetPortfolio(context.Background(), 1)
			if err != nil {
				t.Fatalf("GetPortfolio() error = %v", err)
			}
			cash := dec(tt.cash).InexactFloat64()
			if p.Cash != cash || p.ReservedCash != tt.wantReserved || p.BuyingPower != tt.wantBuyPower {
				t.Errorf("cash/reserved/buying power = %v/%v/%v, want %v/%v/%v", p.Cash, p.ReservedCash, p.BuyingPower, cash, tt.wantReserved, tt.wantBuyPower)
			}
			if p.TotalEquity != cash+1000 {
				t.Errorf("total_equity = %v, want cash + holdings %v (reserved cash is still cash)", p.TotalEquity, cash+1000)
			}
			if got := p.Positions[0].ReservedQuantity; got != tt.wantReservedQty {
				t.Errorf("reserved_quantity = %v, want %v", got, tt.wantReservedQty)
			}
		})
	}
}
