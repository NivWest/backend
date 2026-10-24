package simmarket

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
)

// fixedNow is a Wednesday afternoon well after Epoch, not on a bar boundary.
var fixedNow = time.Date(2026, 3, 18, 14, 37, 42, 0, time.UTC)

func clock(t time.Time) func() time.Time { return func() time.Time { return t } }

func newAt(t time.Time) *Market { return New(clock(t)) }

var ctx = context.Background()

func TestUniverse(t *testing.T) {
	want := []struct{ id, name string }{
		{"SIM-FJRD", "Fjord Robotics"},
		{"SIM-NVE", "Nordvik Energy"},
		{"SIM-KALL", "Kallsten Bank"},
		{"SIM-SOLV", "Solvik Pharma"},
		{"SIM-AURG", "Aurora Gaming"},
		{"SIM-LIND", "Lindqvist Retail"},
		{"SIM-POLT", "Polar Telecom"},
		{"SIM-SKOG", "Skogsberg Timber"},
		{"SIM-HAVN", "Havnes Shipping"},
		{"SIM-MALM", "Malmgren Mining"},
		{"SIM-VIND", "Vindkraft Nord"},
		{"SIM-BRYG", "Brygga Foods"},
	}
	if len(universe) != len(want) {
		t.Fatalf("universe has %d instruments, want %d", len(universe), len(want))
	}
	m := newAt(fixedNow)
	seeds := map[uint64]bool{}
	for i, w := range want {
		in := universe[i]
		if in.orderbookID() != w.id || in.name != w.name {
			t.Errorf("universe[%d] = %s %s, want %s %s", i, in.orderbookID(), in.name, w.id, w.name)
		}
		if seeds[in.seed] {
			t.Errorf("%s: duplicate seed", w.id)
		}
		seeds[in.seed] = true
		if in.s0 < 40 || in.s0 > 600 || in.mu < -0.05 || in.mu > 0.15 || in.sigma < 0.12 || in.sigma > 0.55 {
			t.Errorf("%s: parameters out of range: s0=%v mu=%v sigma=%v", w.id, in.s0, in.mu, in.sigma)
		}
		d, err := m.Details(ctx, w.id)
		if err != nil {
			t.Fatalf("Details(%s): %v", w.id, err)
		}
		if d.Description == "" || d.Sector == "" || d.Currency != "SEK" || d.Market != "Simulated" || d.Symbol != in.symbol {
			t.Errorf("Details(%s) = %+v", w.id, d)
		}
		if d.KeyIndicators.MarketCap == nil || d.KeyIndicators.DividendYieldPct == nil {
			t.Errorf("%s: market cap and dividend yield must be set", w.id)
		}
	}
}

func TestDeterminism(t *testing.T) {
	a, b := newAt(fixedNow), newAt(fixedNow)
	for _, id := range []string{"SIM-FJRD", "SIM-AURG", "SIM-BRYG"} {
		qa, _ := a.Quote(ctx, id)
		qb, _ := b.Quote(ctx, id)
		if qa != qb {
			t.Errorf("%s quotes differ: %+v vs %+v", id, qa, qb)
		}
		for _, p := range domain.CandlePeriods {
			ca, _ := a.Candles(ctx, id, p)
			cb, _ := b.Candles(ctx, id, p)
			if !reflect.DeepEqual(ca, cb) {
				t.Errorf("%s %s candles differ between instances", id, p)
			}
		}
	}
}

func TestLaterClockKeepsEarlierCandles(t *testing.T) {
	cases := []struct {
		period domain.CandlePeriod
		later  time.Duration
		size   time.Duration
	}{
		{domain.Period1D, 2 * time.Hour, 15 * time.Minute},
		{domain.Period1W, 30 * time.Hour, time.Hour},
		{domain.Period1M, 5 * 24 * time.Hour, 24 * time.Hour},
		{domain.Period3M, 20 * 24 * time.Hour, 24 * time.Hour},
		{domain.Period1Y, 30 * 24 * time.Hour, 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		early, _ := newAt(fixedNow).Candles(ctx, "SIM-FJRD", c.period)
		late, _ := newAt(fixedNow.Add(c.later)).Candles(ctx, "SIM-FJRD", c.period)
		byT := map[time.Time]domain.Candle{}
		for _, k := range late {
			byT[k.T] = k
		}
		compared := 0
		for _, k := range early[:len(early)-1] { // the last early bar was partial
			if l, ok := byT[k.T]; ok {
				compared++
				if l != k {
					t.Errorf("%s: bar %s changed: %+v → %+v", c.period, k.T, k, l)
				}
			}
		}
		if compared == 0 {
			t.Errorf("%s: no overlapping bars compared", c.period)
		}
	}
}

func TestCandleShapes(t *testing.T) {
	want := map[domain.CandlePeriod]struct {
		n    int
		size time.Duration
	}{
		domain.Period1D: {96, 15 * time.Minute},
		domain.Period1W: {168, time.Hour},
		domain.Period1M: {30, 24 * time.Hour},
		domain.Period3M: {90, 24 * time.Hour},
		domain.Period1Y: {52, 7 * 24 * time.Hour},
	}
	clocks := []time.Time{fixedNow, time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), time.Date(2027, 6, 7, 23, 59, 59, 0, time.UTC)}
	for _, now := range clocks {
		m := newAt(now)
		for _, id := range []string{"SIM-FJRD", "SIM-SOLV", "SIM-POLT", "SIM-MALM"} {
			for _, p := range domain.CandlePeriods {
				cs, err := m.Candles(ctx, id, p)
				if err != nil {
					t.Fatalf("Candles(%s, %s): %v", id, p, err)
				}
				w := want[p]
				if len(cs) < w.n-1 || len(cs) > w.n+1 {
					t.Errorf("%s %s @%s: %d candles, want %d±1", id, p, now, len(cs), w.n)
				}
				for i, c := range cs {
					if c.T.Location() != time.UTC {
						t.Errorf("%s %s: candle %d not UTC", id, p, i)
					}
					if c.T.Sub(Epoch)%w.size != 0 {
						t.Errorf("%s %s: candle %d at %s not aligned to %s", id, p, i, c.T, w.size)
					}
					if i > 0 && !c.T.After(cs[i-1].T) {
						t.Errorf("%s %s: candle %d not strictly ascending", id, p, i)
					}
					if c.O <= 0 || c.H <= 0 || c.L <= 0 || c.C <= 0 || c.V <= 0 {
						t.Errorf("%s %s: candle %d not positive: %+v", id, p, i, c)
					}
					if c.L > math.Min(c.O, c.C) || c.H < math.Max(c.O, c.C) {
						t.Errorf("%s %s: candle %d inconsistent: %+v", id, p, i, c)
					}
				}
				last := cs[len(cs)-1]
				if now.Before(last.T) || !now.Before(last.T.Add(w.size)) {
					t.Errorf("%s %s: last candle %s does not contain now %s", id, p, last.T, now)
				}
			}
		}
	}
}

func TestCandlesCloseMatchesQuote(t *testing.T) {
	m := newAt(fixedNow)
	q, _ := m.Quote(ctx, "SIM-HAVN")
	for _, p := range domain.CandlePeriods {
		cs, _ := m.Candles(ctx, "SIM-HAVN", p)
		if got := cs[len(cs)-1].C; got != q.Last {
			t.Errorf("%s: last close %v, quote last %v", p, got, q.Last)
		}
	}
}

func TestDailyBarsMatchMinuteWalk(t *testing.T) {
	m := newAt(fixedNow)
	daily, _ := m.Candles(ctx, "SIM-KALL", domain.Period1M)
	s := m.byID["SIM-KALL"]
	day := daily[len(daily)-2] // last complete day
	from := int64(day.T.Sub(Epoch) / time.Minute)
	bars := s.minuteBars(from, from+minutesPerDay-1, minutesPerDay)
	b := bars[0]
	got := domain.Candle{T: day.T, O: domain.RoundPrice(b.open), H: domain.RoundPrice(b.high), L: domain.RoundPrice(b.low), C: domain.RoundPrice(b.close), V: b.volume}
	if got != day {
		t.Errorf("cached day %+v, walked day %+v", day, got)
	}
}

func TestCandlesBeforeEpochOmitted(t *testing.T) {
	m := newAt(Epoch.Add(3*24*time.Hour + 5*time.Hour))
	cs, err := m.Candles(ctx, "SIM-FJRD", domain.Period1M)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 || !cs[0].T.Equal(Epoch) {
		t.Errorf("got %d candles starting %v, want 4 starting at Epoch", len(cs), cs[0].T)
	}
	early, _ := newAt(Epoch.Add(-time.Hour)).Candles(ctx, "SIM-FJRD", domain.Period1D)
	if early == nil || len(early) != 0 {
		t.Errorf("before Epoch: got %v, want empty non-nil", early)
	}
}

func TestPriceBounds(t *testing.T) {
	const days = 3*365 + 1
	for i := range universe {
		s := newSeries(&universe[i])
		t.Run(universe[i].symbol, func(t *testing.T) {
			t.Parallel()
			aggs, _ := s.dayRange(0, days)
			for d, a := range aggs {
				if a.low < 10 || a.high > 5000 {
					t.Fatalf("day %d: low %.2f high %.2f outside [10, 5000]", d, a.low, a.high)
				}
			}
		})
	}
}

func TestQuote(t *testing.T) {
	m := newAt(fixedNow)
	for _, in := range universe {
		q, err := m.Quote(ctx, in.orderbookID())
		if err != nil {
			t.Fatal(err)
		}
		if !(0 < q.Bid && q.Bid < q.Last && q.Last < q.Ask) {
			t.Errorf("%s: bid %v last %v ask %v", q.OrderbookID, q.Bid, q.Last, q.Ask)
		}
		if q.Symbol != in.symbol || q.Currency != "SEK" {
			t.Errorf("%s: %+v", in.symbol, q)
		}
		if !q.UpdatedAt.Equal(time.Date(2026, 3, 18, 14, 37, 0, 0, time.UTC)) || q.UpdatedAt.Location() != time.UTC {
			t.Errorf("%s: updated_at %v", in.symbol, q.UpdatedAt)
		}
		if q.Last != domain.RoundPrice(q.Last) {
			t.Errorf("%s: last %v not rounded", in.symbol, q.Last)
		}
	}
}

func TestChangePercent(t *testing.T) {
	m := newAt(fixedNow)
	s := m.byID["SIM-NVE"]
	_, minute := m.nowMinute()
	last := domain.RoundPrice(s.price(minute))
	ref := s.price(minute - minutesPerDay)
	q, _ := m.Quote(ctx, "SIM-NVE")
	if want := domain.RoundPrice((last - ref) / ref * 100); q.ChangePercent != want {
		t.Errorf("change %v, want %v", q.ChangePercent, want)
	}
}

func TestPinAndClear(t *testing.T) {
	m := newAt(fixedNow)
	before, _ := m.Quote(ctx, "SIM-FJRD")
	candlesBefore, _ := m.Candles(ctx, "SIM-FJRD", domain.Period1D)

	if err := m.SetPrice(ctx, "SIM-FJRD", 100); err != nil {
		t.Fatal(err)
	}
	q, _ := m.Quote(ctx, "SIM-FJRD")
	if q.Last != 100 || q.Bid != 99.9 || q.Ask != 100.1 {
		t.Errorf("pinned quote %+v, want 100 / 99.9 / 100.1", q)
	}
	d, _ := m.Details(ctx, "SIM-FJRD")
	if d.LastPrice != 100 || *d.KeyIndicators.MarketCap != domain.RoundPrice(universe[0].shares*100) {
		t.Errorf("pinned details %+v", d)
	}
	hits, _ := m.Search(ctx, "fjord")
	if hits[0].LastPrice != 100 || hits[0].ChangePercent != q.ChangePercent {
		t.Errorf("pinned search %+v", hits[0])
	}
	candlesPinned, _ := m.Candles(ctx, "SIM-FJRD", domain.Period1D)
	if !reflect.DeepEqual(candlesBefore, candlesPinned) {
		t.Error("candles must ignore pins")
	}
	other, _ := m.Quote(ctx, "SIM-NVE")
	if other.Last == 100 {
		t.Error("pin leaked to another instrument")
	}

	if err := m.SetPrice(ctx, "SIM-FJRD", 123.456); err != nil {
		t.Fatal(err)
	}
	if q, _ := m.Quote(ctx, "SIM-FJRD"); q.Last != 123.46 {
		t.Errorf("pin is not rounded: %v", q.Last)
	}

	if err := m.ClearPrice(ctx, "SIM-FJRD"); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Quote(ctx, "SIM-FJRD")
	if after != before {
		t.Errorf("after clear %+v, want %+v", after, before)
	}
	if err := m.ClearPrice(ctx, "SIM-FJRD"); err != nil {
		t.Errorf("clearing an absent pin: %v", err)
	}
}

func TestSetPriceValidation(t *testing.T) {
	m := newAt(fixedNow)
	for _, p := range []float64{0, -1, 0.001, math.NaN(), math.Inf(1)} {
		err := m.SetPrice(ctx, "SIM-FJRD", p)
		var ve *domain.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("SetPrice(%v) = %v, want ValidationError", p, err)
		}
	}
	var ve *domain.ValidationError
	if err := m.SetPrice(ctx, "SIM-FJRD", -5); errors.As(err, &ve) && ve.Msg != "price must be greater than 0" {
		t.Errorf("message %q", ve.Msg)
	}
}

func TestConcurrentPinAndQuote(t *testing.T) {
	m := newAt(fixedNow)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := universe[i%3].orderbookID()
			for j := range 50 {
				switch j % 4 {
				case 0:
					_ = m.SetPrice(ctx, id, float64(50+j))
				case 1:
					_, _ = m.Quote(ctx, id)
				case 2:
					_, _ = m.Search(ctx, "a")
				default:
					_ = m.ClearPrice(ctx, id)
					_, _ = m.Details(ctx, id)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSearch(t *testing.T) {
	m := newAt(fixedNow)
	hits, err := m.Search(ctx, "fjord")
	if err != nil || len(hits) == 0 {
		t.Fatalf("Search(fjord) = %v, %v", hits, err)
	}
	h := hits[0]
	if h.OrderbookID != "SIM-FJRD" || h.Symbol != "FJRD" || h.Name != "Fjord Robotics" || h.Currency != "SEK" || h.Market != "Simulated" || h.LastPrice <= 0 {
		t.Errorf("first hit %+v", h)
	}

	if hits, _ := m.Search(ctx, "FJRD"); len(hits) != 1 || hits[0].OrderbookID != "SIM-FJRD" {
		t.Errorf("Search(FJRD) = %+v", hits)
	}
	if hits, _ := m.Search(ctx, "sim-nve"); len(hits) == 0 || hits[0].OrderbookID != "SIM-NVE" {
		t.Errorf("Search(sim-nve) = %+v", hits)
	}
	// "nord" is an exact symbol of nothing, a name prefix of Nordvik Energy and inside Vindkraft Nord.
	hits, _ = m.Search(ctx, "nord")
	if len(hits) != 2 || hits[0].OrderbookID != "SIM-NVE" || hits[1].OrderbookID != "SIM-VIND" {
		t.Errorf("Search(nord) = %+v", hits)
	}
	// "sim" matches every id; ordered by name.
	hits, _ = m.Search(ctx, "SIM")
	if len(hits) != 12 {
		t.Fatalf("Search(SIM) returned %d", len(hits))
	}
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Name > hits[i].Name {
			t.Errorf("Search(SIM) not ordered by name at %d", i)
		}
	}
	none, err := m.Search(ctx, "zzz-nothing")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("Search(nothing) = %#v, %v; want empty non-nil", none, err)
	}
}

func TestUnknownInstrument(t *testing.T) {
	m := newAt(fixedNow)
	checks := map[string]error{}
	_, checks["Quote"] = m.Quote(ctx, "SIM-NOPE")
	_, checks["Details"] = m.Details(ctx, "SIM-NOPE")
	_, checks["Candles"] = m.Candles(ctx, "SIM-NOPE", domain.Period1M)
	checks["SetPrice"] = m.SetPrice(ctx, "SIM-NOPE", 10)
	checks["ClearPrice"] = m.ClearPrice(ctx, "SIM-NOPE")
	for name, err := range checks {
		if !errors.Is(err, domain.ErrInstrumentNotFound) {
			t.Errorf("%s: %v, want ErrInstrumentNotFound", name, err)
		}
	}
}

func TestKeyIndicators(t *testing.T) {
	m := newAt(fixedNow)
	solv, _ := m.Details(ctx, "SIM-SOLV")
	if solv.KeyIndicators.PERatio != nil {
		t.Errorf("SIM-SOLV pe_ratio = %v, want nil", *solv.KeyIndicators.PERatio)
	}
	f, _ := m.Details(ctx, "SIM-FJRD")
	k := f.KeyIndicators
	if k.PERatio == nil || k.DividendYieldPct == nil || k.MarketCap == nil {
		t.Fatalf("SIM-FJRD key indicators %+v", k)
	}
	in := universe[0]
	if *k.PERatio != domain.RoundPrice(f.LastPrice/in.eps) || *k.DividendYieldPct <= 0 ||
		*k.DividendYieldPct != domain.RoundPrice(in.dps/f.LastPrice*100) || *k.MarketCap != domain.RoundPrice(in.shares*f.LastPrice) {
		t.Errorf("SIM-FJRD key indicators %v %v %v at last %v", *k.PERatio, *k.DividendYieldPct, *k.MarketCap, f.LastPrice)
	}
	kall, _ := m.Details(ctx, "SIM-KALL")
	if y := *kall.KeyIndicators.DividendYieldPct; y < 3 || y > 8 {
		t.Errorf("Kallsten Bank yield %v", y)
	}
}

func TestNilClockUsesTimeNow(t *testing.T) {
	q, err := New(nil).Quote(ctx, "SIM-FJRD")
	if err != nil || time.Since(q.UpdatedAt) > 2*time.Minute {
		t.Errorf("New(nil) quote %+v, %v", q, err)
	}
}
