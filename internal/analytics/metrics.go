// Package analytics holds the pure risk metrics of story NW-10: daily series, daily returns, annualized
// volatility, Sharpe ratio, maximum drawdown and total return. It does no I/O and knows nothing about
// users, snapshots or HTTP; services feed it equity values and round its results at the edge.
//
// Every function works on fractions (0.0123 = 1.23%); the caller converts to percent. Statistics use the
// sample standard deviation (n − 1), and annualization assumes TradingDaysPerYear trading days.
package analytics

import (
	"math"
	"sort"
	"time"
)

// TradingDaysPerYear annualizes daily statistics (√252 for volatility and Sharpe, rf/252 per day).
const TradingDaysPerYear = 252

// MinDailyReturns is the fewest daily returns for which volatility, Sharpe and drawdown are reported.
const MinDailyReturns = 5

// Point is one equity observation at time T.
type Point struct {
	T      time.Time
	Equity float64
}

// DailySeries keeps the last point of every UTC calendar day, oldest day first. The input may be in any
// order; ties at the same instant keep the one that comes later in the input.
func DailySeries(points []Point) []Point {
	if len(points) == 0 {
		return []Point{}
	}
	sorted := make([]Point, len(points))
	copy(sorted, points)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].T.Before(sorted[j].T) })

	out := make([]Point, 0, len(sorted))
	for _, p := range sorted {
		if n := len(out); n > 0 && sameUTCDay(out[n-1].T, p.T) {
			out[n-1] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

func sameUTCDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

// Equities returns the equity values of points in order.
func Equities(points []Point) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = p.Equity
	}
	return out
}

// DailyReturns returns r_d = equity_d / equity_(d−1) − 1 for every consecutive pair, so len(equity) − 1
// values (none for fewer than two). A non-positive previous equity has no defined return; that pair is
// skipped.
func DailyReturns(equity []float64) []float64 {
	if len(equity) < 2 {
		return []float64{}
	}
	out := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] <= 0 {
			continue
		}
		out = append(out, equity[i]/equity[i-1]-1)
	}
	return out
}

// mean and sampleStdDev return ok=false when they are undefined (no values, or fewer than two).
func mean(xs []float64) (float64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs)), true
}

func sampleStdDev(xs []float64) (float64, bool) {
	if len(xs) < 2 {
		return 0, false
	}
	m, _ := mean(xs)
	ss := 0.0
	for _, x := range xs {
		d := x - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)-1)), true
}

// AnnualizedVolatility is stdev(returns) × √252 as a fraction. ok is false for fewer than two returns.
func AnnualizedVolatility(returns []float64) (vol float64, ok bool) {
	sd, ok := sampleStdDev(returns)
	if !ok {
		return 0, false
	}
	return sd * math.Sqrt(TradingDaysPerYear), true
}

// Sharpe is (mean(returns) − riskFree/252) / stdev(returns) × √252, riskFree being the annual risk-free
// rate as a fraction (0.02). ok is false for fewer than two returns or a zero standard deviation, where the
// ratio is undefined.
func Sharpe(returns []float64, riskFree float64) (ratio float64, ok bool) {
	sd, ok := sampleStdDev(returns)
	if !ok || sd == 0 {
		return 0, false
	}
	m, _ := mean(returns)
	return (m - riskFree/TradingDaysPerYear) / sd * math.Sqrt(TradingDaysPerYear), true
}

// MaxDrawdown is the largest peak-to-trough decline of equity as a non-positive fraction (−0.2 is a 20%
// drawdown); 0 when equity never falls below an earlier peak or there are no values. Non-positive peaks
// are ignored, since a decline from them is undefined.
func MaxDrawdown(equity []float64) float64 {
	peak, worst := 0.0, 0.0
	for _, e := range equity {
		if e > peak {
			peak = e
		}
		if peak > 0 {
			if dd := e/peak - 1; dd < worst {
				worst = dd
			}
		}
	}
	return worst
}

// TotalReturn is end / start − 1 as a fraction; 0 when start is not positive.
func TotalReturn(start, end float64) float64 {
	if start <= 0 {
		return 0
	}
	return end/start - 1
}
