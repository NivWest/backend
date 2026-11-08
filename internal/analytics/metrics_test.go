package analytics

import (
	"math"
	"testing"
	"time"
)

// Golden series: seven daily closes, so six daily returns (one more than MinDailyReturns).
//
//	equity  = 100000, 101000, 99500, 100500, 102000, 101500, 103000
//	r_d     = 101000/100000−1 =  0.01
//	          99500/101000−1  = −0.014851485148514865
//	          100500/99500−1  =  0.01005025125628145
//	          102000/100500−1 =  0.014925373134328401
//	          101500/102000−1 = −0.004901960784313708
//	          103000/101500−1 =  0.014778325123152802
//	mean    = Σ r_d / 6 = 0.005000083930155681
//	stdev   = √(Σ (r_d − mean)² / 5) = 0.012138896175145653   (sample, n − 1)
//	vol     = stdev × √252 = 0.19269900282161134
//	sharpe  = (mean − 0.02/252) / stdev × √252 = 6.435015917270551
//	max dd  = 99500 / 101000 − 1 = −0.014851485148514865   (peak 101000 on day 2, trough 99500 on day 3)
//	total   = 103000 / 100000 − 1 = 0.03
//
// Computed with Python 3's statistics module (statistics.mean, statistics.stdev) and math.sqrt; pasted here.
var goldenEquity = []float64{100000, 101000, 99500, 100500, 102000, 101500, 103000}

const (
	goldenMean   = 0.005000083930155681
	goldenStdev  = 0.012138896175145653
	goldenVol    = 0.19269900282161134
	goldenSharpe = 6.435015917270551
	goldenMaxDD  = -0.014851485148514865
	goldenTotal  = 0.03
	tolerance    = 1e-12
)

var goldenReturns = []float64{
	0.010000000000000009,
	-0.014851485148514865,
	0.01005025125628145,
	0.014925373134328401,
	-0.004901960784313708,
	0.014778325123152802,
}

func near(a, b float64) bool { return math.Abs(a-b) <= tolerance }

func TestDailyReturns(t *testing.T) {
	tests := []struct {
		name   string
		equity []float64
		want   []float64
	}{
		{"golden series", goldenEquity, goldenReturns},
		{"no values", nil, []float64{}},
		{"one value has no return", []float64{100000}, []float64{}},
		{"flat", []float64{100, 100, 100}, []float64{0, 0}},
		{"zero previous equity is skipped", []float64{0, 100, 110}, []float64{0.10000000000000009}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DailyReturns(tt.equity)
			if got == nil {
				t.Fatal("DailyReturns returned nil, want a non-nil slice")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if !near(got[i], tt.want[i]) {
					t.Errorf("r[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMeanAndStdDevGolden(t *testing.T) {
	m, ok := mean(goldenReturns)
	if !ok || !near(m, goldenMean) {
		t.Errorf("mean = %v (ok %v), want %v", m, ok, goldenMean)
	}
	sd, ok := sampleStdDev(goldenReturns)
	if !ok || !near(sd, goldenStdev) {
		t.Errorf("stdev = %v (ok %v), want %v", sd, ok, goldenStdev)
	}
}

func TestAnnualizedVolatility(t *testing.T) {
	tests := []struct {
		name    string
		returns []float64
		want    float64
		wantOK  bool
	}{
		{"golden", goldenReturns, goldenVol, true},
		{"flat returns have zero volatility", []float64{0, 0, 0, 0, 0}, 0, true},
		{"one return is undefined", []float64{0.01}, 0, false},
		{"none is undefined", nil, 0, false},
		// stdev of {0.01, −0.01} = √((0.01² + 0.01²)/1) = 0.01414213562373095; × √252 = 0.2244994432064365.
		{"two returns", []float64{0.01, -0.01}, 0.2244994432064365, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := AnnualizedVolatility(tt.returns)
			if ok != tt.wantOK || !near(got, tt.want) {
				t.Errorf("AnnualizedVolatility = %v (ok %v), want %v (ok %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSharpe(t *testing.T) {
	tests := []struct {
		name     string
		returns  []float64
		riskFree float64
		want     float64
		wantOK   bool
	}{
		{"golden with rf 2%", goldenReturns, 0.02, goldenSharpe, true},
		// Without rf: mean / stdev × √252 (Python: statistics.mean(r) / statistics.stdev(r) * math.sqrt(252)).
		{"golden with rf 0", goldenReturns, 0, 6.538804726279152, true},
		{"zero volatility is undefined", []float64{0.001, 0.001, 0.001, 0.001, 0.001}, 0.02, 0, false},
		{"one return is undefined", []float64{0.01}, 0.02, 0, false},
		// mean 0, stdev 0.01414213562373095: (0 − 0.02/252) / 0.01414… × √252 = −0.08908708063747482.
		{"negative excess return", []float64{0.01, -0.01}, 0.02, -0.0890870806374748, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Sharpe(tt.returns, tt.riskFree)
			if ok != tt.wantOK || !near(got, tt.want) {
				t.Errorf("Sharpe = %v (ok %v), want %v (ok %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestMaxDrawdown(t *testing.T) {
	tests := []struct {
		name   string
		equity []float64
		want   float64
	}{
		{"golden", goldenEquity, goldenMaxDD},
		// Peak 110000, later trough 88000: 88000/110000 − 1 = −0.2 (the earlier dip to 99000 is only −0.1).
		{"deepest of several dips", []float64{100000, 110000, 99000, 105000, 88000, 120000}, -0.19999999999999996},
		{"only rising", []float64{1, 2, 3}, 0},
		{"empty", nil, 0},
		{"first value is the peak", []float64{100, 50}, -0.5},
		{"non-positive start is ignored", []float64{0, 100, 75}, -0.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaxDrawdown(tt.equity); !near(got, tt.want) {
				t.Errorf("MaxDrawdown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTotalReturn(t *testing.T) {
	tests := []struct {
		name       string
		start, end float64
		want       float64
	}{
		{"golden", 100000, 103000, goldenTotal},
		{"unchanged", 100000, 100000, 0},
		// AC2 of NW-10: 99964.97 / 100000 − 1 = −0.0003503.
		{"after one buy", 100000, 99964.97, -0.0003503000000000256},
		{"zero start", 0, 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TotalReturn(tt.start, tt.end); !near(got, tt.want) {
				t.Errorf("TotalReturn = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDailySeries(t *testing.T) {
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC) }
	cet := time.FixedZone("CET", 3600)
	tests := []struct {
		name   string
		points []Point
		want   []Point
	}{
		{"empty", nil, []Point{}},
		{
			"last of each UTC day, input unsorted",
			[]Point{{at(2, 9, 0), 2}, {at(1, 9, 0), 1}, {at(1, 23, 59), 11}, {at(2, 8, 0), 3}, {at(4, 0, 0), 4}},
			[]Point{{at(1, 23, 59), 11}, {at(2, 9, 0), 2}, {at(4, 0, 0), 4}},
		},
		{
			// 00:30 CET on Nov 3 is 23:30 UTC on Nov 2: it belongs to Nov 2.
			"days are UTC days, not local ones",
			[]Point{{at(2, 12, 0), 1}, {time.Date(2026, 11, 3, 0, 30, 0, 0, cet), 2}},
			[]Point{{time.Date(2026, 11, 3, 0, 30, 0, 0, cet), 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DailySeries(tt.points)
			if got == nil {
				t.Fatal("DailySeries returned nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if !got[i].T.Equal(tt.want[i].T) || got[i].Equity != tt.want[i].Equity {
					t.Errorf("[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEquities(t *testing.T) {
	got := Equities([]Point{{Equity: 1}, {Equity: 2.5}})
	if len(got) != 2 || got[0] != 1 || got[1] != 2.5 {
		t.Errorf("Equities = %v", got)
	}
}
