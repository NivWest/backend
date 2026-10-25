package simmarket

import (
	"math"
	"sync"
)

// The price path is a seeded geometric Brownian motion on 1-minute steps from Epoch:
//
//	price(m) = S0 · exp((μ − σ²/2)·m·dt + σ·√dt·W(m)),   W(m) = Σ_{k=1..m} z(k),   dt = 1/525600
//
// z(k) is a standard normal derived only from the instrument seed and k, so a given (instrument,
// minute) always has the same price. W is always accumulated in the same order (minute by minute
// from Epoch), which keeps the floating point result identical however the minute is reached.

const (
	minutesPerDay  = 1440
	minutesPerYear = 525600
	dt             = 1.0 / minutesPerYear

	minVolume   = 100
	volumeRange = 4901 // per-minute volume is minVolume..minVolume+volumeRange-1, times volScale
)

// splitmix64 is a stateless 64-bit mixer (Steele, Lea, Flood 2014).
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// draw returns the standard normal z and the traded volume for minute k of the instrument with this seed.
func draw(seed uint64, k int64, volScale int64) (z float64, vol int64) {
	h1 := splitmix64(seed ^ (uint64(k) * 0xD1B54A32D192ED03))
	h2 := splitmix64(h1)
	h3 := splitmix64(h2)
	u1 := (float64(h1>>11) + 1) / (1 << 53) // (0, 1]
	u2 := float64(h2>>11) / (1 << 53)       // [0, 1)
	z = math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	vol = (minVolume + int64(h3%volumeRange)) * volScale
	return z, vol
}

// dayAgg is the cached aggregate of one complete UTC day (minutes d·1440 … d·1440+1439).
type dayAgg struct {
	w0                     float64 // W at the first minute of the day
	open, high, low, close float64 // unrounded prices
	volume                 int64
}

// bar accumulates OHLCV over consecutive minutes.
type bar struct {
	open, high, low, close float64
	volume                 int64
	n                      int
}

func (b *bar) addPrice(p float64, v int64) {
	if b.n == 0 {
		b.open, b.high, b.low = p, p, p
	}
	b.high = math.Max(b.high, p)
	b.low = math.Min(b.low, p)
	b.close = p
	b.volume += v
	b.n++
}

func (b *bar) addDay(d dayAgg) {
	if b.n == 0 {
		b.open, b.high, b.low = d.open, d.high, d.low
	}
	b.high = math.Max(b.high, d.high)
	b.low = math.Min(b.low, d.low)
	b.close = d.close
	b.volume += d.volume
	b.n++
}

// series is the price path of one instrument, with a cache of daily aggregates.
type series struct {
	inst *instrument
	lnS0 float64 // ln S0
	a    float64 // (μ − σ²/2)·dt, drift per minute in log space
	b    float64 // σ·√dt

	mu     sync.Mutex
	days   []dayAgg // days[d] for every computed complete day 0…len(days)-1
	nextW0 float64  // W at the first minute of day len(days)
}

func newSeries(in *instrument) *series {
	return &series{
		inst: in,
		lnS0: math.Log(in.s0),
		a:    (in.mu - in.sigma*in.sigma/2) * dt,
		b:    in.sigma * math.Sqrt(dt),
	}
}

func (s *series) priceAt(m int64, w float64) float64 {
	return math.Exp(s.lnS0 + s.a*float64(m) + s.b*w)
}

// walk visits minutes from..to (inclusive, same or later day allowed) starting from w = W(from),
// calling fn with each minute's price and volume.
func (s *series) walk(from, to int64, w float64, fn func(m int64, p float64, v int64)) {
	for m := from; m <= to; m++ {
		z, v := draw(s.inst.seed, m, s.inst.volScale)
		if m > from {
			w += z
		}
		fn(m, s.priceAt(m, w), v)
	}
}

// ensureLocked computes the aggregates of days 0…n-1. Callers hold s.mu.
func (s *series) ensureLocked(n int64) {
	for int64(len(s.days)) < n {
		d := int64(len(s.days))
		start := d * minutesPerDay
		agg := dayAgg{w0: s.nextW0}
		var b bar
		w := s.nextW0
		for m := start; m < start+minutesPerDay; m++ {
			z, v := draw(s.inst.seed, m, s.inst.volScale)
			if m > start {
				w += z
			}
			b.addPrice(s.priceAt(m, w), v)
		}
		z, _ := draw(s.inst.seed, start+minutesPerDay, s.inst.volScale)
		s.nextW0 = w + z
		agg.open, agg.high, agg.low, agg.close, agg.volume = b.open, b.high, b.low, b.close, b.volume
		s.days = append(s.days, agg)
	}
}

// dayStartW returns W at the first minute of day d (d >= 0), computing the days before it.
func (s *series) dayStartW(d int64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked(d)
	if d < int64(len(s.days)) {
		return s.days[d].w0
	}
	return s.nextW0
}

// days returns the aggregates of complete days from..to (inclusive) and W at the start of day to+1.
func (s *series) dayRange(from, to int64) ([]dayAgg, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked(to + 1)
	out := make([]dayAgg, 0, to-from+1)
	if from <= to {
		out = append(out, s.days[from:to+1]...)
	}
	w := s.nextW0
	if to+1 < int64(len(s.days)) {
		w = s.days[to+1].w0
	}
	return out, w
}

// wAt returns W(m) for m >= 0.
func (s *series) wAt(m int64) float64 {
	d := m / minutesPerDay
	w := s.dayStartW(d)
	for k := d*minutesPerDay + 1; k <= m; k++ {
		z, _ := draw(s.inst.seed, k, s.inst.volScale)
		w += z
	}
	return w
}

// price returns the unrounded path price at minute m; minutes before Epoch use the Epoch price.
func (s *series) price(m int64) float64 {
	if m < 0 {
		m = 0
	}
	return s.priceAt(m, s.wAt(m))
}

// minuteBars aggregates minutes from..to (from >= 0, from aligned to size) into bars of size minutes.
func (s *series) minuteBars(from, to, size int64) []bar {
	if to < from {
		return nil
	}
	bars := make([]bar, (to-from)/size+1)
	s.walk(from, to, s.wAt(from), func(m int64, p float64, v int64) {
		bars[(m-from)/size].addPrice(p, v)
	})
	return bars
}

// dayBars aggregates days fromDay..nowDay (fromDay >= 0, aligned to size) into bars of size days.
// Days before nowDay come from the cache; nowDay is walked up to nowMinute.
func (s *series) dayBars(fromDay, nowMinute, size int64) []bar {
	nowDay := nowMinute / minutesPerDay
	if nowDay < fromDay {
		return nil
	}
	bars := make([]bar, (nowDay-fromDay)/size+1)
	full, w := s.dayRange(fromDay, nowDay-1)
	for i, d := range full {
		bars[int64(i)/size].addDay(d)
	}
	last := &bars[(nowDay-fromDay)/size]
	s.walk(nowDay*minutesPerDay, nowMinute, w, func(_ int64, p float64, v int64) {
		last.addPrice(p, v)
	})
	return bars
}
