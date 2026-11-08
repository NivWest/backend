package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

// analyticsNow is the fixed "now" of these tests (a Sunday afternoon, UTC).
var analyticsNow = time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)

type portfolioStub struct {
	p   *domain.Portfolio
	err error
}

func (s portfolioStub) GetPortfolio(context.Context, uint) (*domain.Portfolio, error) {
	return s.p, s.err
}

func cashOnly(cash float64) *domain.Portfolio {
	return &domain.Portfolio{Cash: cash, TotalEquity: cash, Positions: []domain.PortfolioPosition{}, TopMovers: []domain.PortfolioPosition{}}
}

func snapshot(userID uint, t time.Time, equity string) models.EquitySnapshot {
	return models.EquitySnapshot{UserID: userID, T: t, Cash: dec(equity), HoldingsValue: dec("0"), VaultValue: dec("0"), Equity: dec(equity)}
}

func newAnalytics(repo *testutil.SnapshotRepo, p *domain.Portfolio) *AnalyticsService {
	return NewAnalyticsService(repo, portfolioStub{p: p}, domain.DefaultRiskFreeRate, func() time.Time { return analyticsNow })
}

func TestAnalyticsService_GetHistory_Ranges(t *testing.T) {
	h := func(hoursAgo float64) time.Time {
		return analyticsNow.Add(-time.Duration(hoursAgo * float64(time.Hour)))
	}
	repo := &testutil.SnapshotRepo{Snapshots: []models.EquitySnapshot{
		snapshot(1, h(24*40), "90000"),      // 40 days ago: only in all
		snapshot(1, h(24*10), "95000"),      // Oct 29 15:00: 1m and all
		snapshot(1, h(24*10-1.25), "95500"), // Oct 29 16:15: another hour of the same day
		snapshot(1, h(30), "99000"),         // 30 h ago: 1w, 1m, all
		snapshot(1, h(2.5), "99800"),        // 12:30
		snapshot(1, h(2.25), "99900"),       // 12:45: same hour as 12:30
		snapshot(1, h(1), "100000"),         // 14:00
		snapshot(2, h(1), "1"),              // another user: never returned
	}}
	tests := []struct {
		rawRange string
		want     domain.HistoryRange
		equities []float64
	}{
		{"", domain.HistoryRange1D, []float64{99800, 99900, 100000}},
		{"1d", domain.HistoryRange1D, []float64{99800, 99900, 100000}},
		{"1w", domain.HistoryRange1W, []float64{99000, 99900, 100000}},
		{"1m", domain.HistoryRange1M, []float64{95000, 95500, 99000, 99900, 100000}},
		// daily, last of each UTC day: Sep 29, Oct 29 (16:15 beats 15:00), Nov 7, Nov 8 (14:00)
		{"all", domain.HistoryRangeAll, []float64{90000, 95500, 99000, 100000}},
	}
	for _, tt := range tests {
		t.Run("range "+tt.rawRange, func(t *testing.T) {
			got, err := newAnalytics(repo, nil).GetHistory(context.Background(), 1, tt.rawRange)
			if err != nil {
				t.Fatalf("GetHistory() error = %v", err)
			}
			if got.Range != tt.want {
				t.Errorf("Range = %q, want %q", got.Range, tt.want)
			}
			eq := make([]float64, len(got.Points))
			for i, p := range got.Points {
				eq[i] = p.Equity
				if p.T.Location() != time.UTC {
					t.Errorf("point %d not UTC: %v", i, p.T)
				}
				if i > 0 && !got.Points[i-1].T.Before(p.T) {
					t.Errorf("points not oldest first at %d", i)
				}
			}
			if !equalFloats(eq, tt.equities) {
				t.Errorf("equities = %v, want %v", eq, tt.equities)
			}
		})
	}
}

func TestAnalyticsService_GetHistory_Errors(t *testing.T) {
	for _, bad := range []string{"1y", "1D", "ALL", "3m", " 1d", "24h"} {
		t.Run(bad, func(t *testing.T) {
			_, err := newAnalytics(&testutil.SnapshotRepo{}, nil).GetHistory(context.Background(), 1, bad)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) || verr.Msg != "range must be one of 1d, 1w, 1m, all" {
				t.Errorf("error = %v, want the range ValidationError", err)
			}
		})
	}

	dbDown := errors.New("db down")
	repo := &testutil.SnapshotRepo{ListByUserFn: func(context.Context, uint, time.Time) ([]models.EquitySnapshot, error) { return nil, dbDown }}
	if _, err := newAnalytics(repo, nil).GetHistory(context.Background(), 1, "1d"); !errors.Is(err, dbDown) {
		t.Errorf("error = %v, want %v", err, dbDown)
	}
}

func TestAnalyticsService_GetHistory_EmptyIsNonNilArray(t *testing.T) {
	got, err := newAnalytics(&testutil.SnapshotRepo{}, nil).GetHistory(context.Background(), 1, "all")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `{"range":"all","points":[]}` {
		t.Errorf("JSON = %s", raw)
	}
}

func TestAnalyticsService_GetHistory_PassesWindowLowerBound(t *testing.T) {
	var since []time.Time
	repo := &testutil.SnapshotRepo{ListByUserFn: func(_ context.Context, _ uint, s time.Time) ([]models.EquitySnapshot, error) {
		since = append(since, s)
		return []models.EquitySnapshot{}, nil
	}}
	svc := newAnalytics(repo, nil)
	for _, r := range []string{"1d", "1w", "1m", "all"} {
		if _, err := svc.GetHistory(context.Background(), 1, r); err != nil {
			t.Fatal(err)
		}
	}
	want := []time.Time{analyticsNow.Add(-24 * time.Hour), analyticsNow.Add(-7 * 24 * time.Hour), analyticsNow.Add(-30 * 24 * time.Hour), {}}
	for i := range want {
		if !since[i].Equal(want[i]) {
			t.Errorf("since[%d] = %v, want %v", i, since[i], want[i])
		}
	}
}

func TestAnalyticsService_GetAnalytics_FreshAccount(t *testing.T) {
	got, err := newAnalytics(&testutil.SnapshotRepo{}, cashOnly(100000)).GetAnalytics(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	want := `{"total_return_pct":0,"volatility_annualized_pct":null,"sharpe_ratio":null,"max_drawdown_pct":null,` +
		`"sufficient_data":false,"data_points":0,"allocation":[{"label":"Cash","orderbook_id":null,"value":100000,"weight":100}]}`
	if string(raw) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", raw, want)
	}
}

// day returns 16:00 UTC on November d.
func day(d int) time.Time { return time.Date(2026, 11, d, 16, 0, 0, 0, time.UTC) }

func TestAnalyticsService_GetAnalytics_Metrics(t *testing.T) {
	// The golden series of internal/analytics (seven daily closes, six returns), with intraday snapshots
	// that the daily series must drop (only the last of each UTC day counts).
	golden := []string{"100000", "101000", "99500", "100500", "102000", "101500", "103000"}
	var snaps []models.EquitySnapshot
	for i, eq := range golden {
		snaps = append(snaps, snapshot(1, day(i+1).Add(-3*time.Hour), "1"), snapshot(1, day(i+1), eq))
	}
	repo := &testutil.SnapshotRepo{Snapshots: snaps}

	got, err := newAnalytics(repo, cashOnly(103000)).GetAnalytics(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// vol 0.19269900282161134 → 19.27; sharpe 6.435015917270551 → 6.44; max dd −0.014851… → −1.49; total 3.
	if !got.SufficientData || got.DataPoints != 6 {
		t.Fatalf("sufficient %v, data_points %d; want true, 6", got.SufficientData, got.DataPoints)
	}
	checkPtr(t, "volatility", got.VolatilityAnnualizedPct, 19.27)
	checkPtr(t, "sharpe", got.SharpeRatio, 6.44)
	checkPtr(t, "max drawdown", got.MaxDrawdownPct, -1.49)
	if got.TotalReturnPct != 3 {
		t.Errorf("total_return_pct = %v, want 3", got.TotalReturnPct)
	}
}

func TestAnalyticsService_GetAnalytics_Thresholds(t *testing.T) {
	series := func(n int, flat bool) []models.EquitySnapshot {
		var out []models.EquitySnapshot
		for i := range n {
			eq := "100000"
			if !flat && i%2 == 1 {
				eq = "101000"
			}
			out = append(out, snapshot(1, day(i+1), eq))
		}
		return out
	}
	tests := []struct {
		name           string
		snaps          []models.EquitySnapshot
		wantPoints     int
		wantSufficient bool
		wantSharpe     bool
		wantVol        *float64
	}{
		{"one snapshot has no return", series(1, false), 0, false, false, nil},
		{"4 returns are not enough", series(5, false), 4, false, false, nil},
		{"5 returns are enough", series(6, false), 5, true, true, nil},
		// Flat equity: volatility 0, Sharpe undefined (null) although the data suffices; drawdown 0.
		{"flat equity has no Sharpe", series(6, true), 5, true, false, ptr(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAnalytics(&testutil.SnapshotRepo{Snapshots: tt.snaps}, cashOnly(100000)).GetAnalytics(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if got.DataPoints != tt.wantPoints || got.SufficientData != tt.wantSufficient {
				t.Fatalf("data_points %d sufficient %v; want %d %v", got.DataPoints, got.SufficientData, tt.wantPoints, tt.wantSufficient)
			}
			if (got.SharpeRatio != nil) != tt.wantSharpe {
				t.Errorf("sharpe = %v, want present %v", got.SharpeRatio, tt.wantSharpe)
			}
			if !tt.wantSufficient && (got.VolatilityAnnualizedPct != nil || got.MaxDrawdownPct != nil) {
				t.Errorf("metrics must be null without sufficient data: %+v", got)
			}
			if tt.wantSufficient && (got.VolatilityAnnualizedPct == nil || got.MaxDrawdownPct == nil) {
				t.Errorf("volatility and drawdown must be set: %+v", got)
			}
			if tt.wantVol != nil {
				checkPtr(t, "volatility", got.VolatilityAnnualizedPct, *tt.wantVol)
				checkPtr(t, "max drawdown", got.MaxDrawdownPct, 0)
			}
		})
	}
}

func TestAnalyticsService_GetAnalytics_Allocation(t *testing.T) {
	pos := func(id, symbol string, value float64) domain.PortfolioPosition {
		return domain.PortfolioPosition{OrderbookID: id, Symbol: symbol, MarketValue: value}
	}
	tests := []struct {
		name        string
		portfolio   *domain.Portfolio
		wantLabels  []string
		wantWeights []float64
		wantReturn  float64
	}{
		{
			// AC2: 100 SIM-FJRD at last 100 (10,000.00) after a 10,035.03 buy: equity 99,964.97.
			// 10000 / 99964.97 = 10.0035% → 10.00; cash 89.9965% → 90.00.
			name: "ac2 one position and cash",
			portfolio: &domain.Portfolio{Cash: 89964.97, HoldingsValue: 10000, TotalEquity: 99964.97,
				Positions: []domain.PortfolioPosition{pos("SIM-FJRD", "FJRD", 10000)}},
			wantLabels: []string{"FJRD", "Cash"}, wantWeights: []float64{10, 90}, wantReturn: -0.04,
		},
		{
			// Thirds: 33.33 × 3 = 99.99, the remainder 0.01 goes to the largest slice (the first of equals).
			name: "rounding remainder goes to the largest slice",
			portfolio: &domain.Portfolio{Cash: 100, TotalEquity: 300,
				Positions: []domain.PortfolioPosition{pos("SIM-A", "A", 100), pos("SIM-B", "B", 100)}},
			wantLabels: []string{"A", "B", "Cash"}, wantWeights: []float64{33.34, 33.33, 33.33}, wantReturn: -99.7,
		},
		{
			name:       "all invested keeps a zero cash slice",
			portfolio:  &domain.Portfolio{Cash: 0, TotalEquity: 120000, Positions: []domain.PortfolioPosition{pos("SIM-A", "A", 120000)}},
			wantLabels: []string{"A", "Cash"}, wantWeights: []float64{100, 0}, wantReturn: 20,
		},
		{
			name:       "zero equity has zero weights",
			portfolio:  &domain.Portfolio{Cash: 0, TotalEquity: 0, Positions: []domain.PortfolioPosition{}},
			wantLabels: []string{"Cash"}, wantWeights: []float64{0}, wantReturn: -100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAnalytics(&testutil.SnapshotRepo{}, tt.portfolio).GetAnalytics(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if got.TotalReturnPct != tt.wantReturn {
				t.Errorf("total_return_pct = %v, want %v", got.TotalReturnPct, tt.wantReturn)
			}
			if len(got.Allocation) != len(tt.wantLabels) {
				t.Fatalf("allocation = %+v", got.Allocation)
			}
			sum := 0.0
			for i, s := range got.Allocation {
				if s.Label != tt.wantLabels[i] || s.Weight != tt.wantWeights[i] {
					t.Errorf("[%d] = %s %v, want %s %v", i, s.Label, s.Weight, tt.wantLabels[i], tt.wantWeights[i])
				}
				if (s.Label == domain.CashLabel) != (s.OrderbookID == nil) {
					t.Errorf("[%d] orderbook_id = %v: nil exactly for cash", i, s.OrderbookID)
				}
				sum += s.Weight
			}
			if tt.portfolio.TotalEquity > 0 && math.Abs(sum-100) > 1e-9 {
				t.Errorf("weights sum to %v, want 100", sum)
			}
		})
	}
}

func TestAnalyticsService_GetAnalytics_Errors(t *testing.T) {
	dbDown := errors.New("db down")
	svc := NewAnalyticsService(&testutil.SnapshotRepo{}, portfolioStub{err: domain.ErrUserNotFound}, 0.02, nil)
	if _, err := svc.GetAnalytics(context.Background(), 1); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("portfolio error = %v", err)
	}
	repo := &testutil.SnapshotRepo{ListByUserFn: func(context.Context, uint, time.Time) ([]models.EquitySnapshot, error) { return nil, dbDown }}
	if _, err := newAnalytics(repo, cashOnly(1)).GetAnalytics(context.Background(), 1); !errors.Is(err, dbDown) {
		t.Errorf("snapshot error = %v", err)
	}
}

func TestRound2(t *testing.T) {
	for in, want := range map[float64]float64{1.005: 1.01, -1.005: -1.01, 2.344: 2.34, -0.001: 0, 19.269900282: 19.27} {
		if got := round2(in); got != want || math.Signbit(got) != math.Signbit(want) {
			t.Errorf("round2(%v) = %v, want %v", in, got, want)
		}
	}
}

func checkPtr(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
