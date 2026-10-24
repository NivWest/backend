// Package simmarket is a deterministic, simulated market: twelve fictional Swedish companies whose
// prices follow a seeded geometric Brownian motion on 1-minute steps from Epoch. It implements
// domain.MarketDataProvider and domain.MarketSimControl (dev/test price pinning).
package simmarket

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"backend/internal/domain"
)

var (
	_ domain.MarketDataProvider = (*Market)(nil)
	_ domain.MarketSimControl   = (*Market)(nil)
)

// Epoch is minute 0 of every price path (a Monday, so weekly bars start on Mondays).
var Epoch = time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

const maxSearchResults = 20

// Market is the simulated market. It is safe for concurrent use.
type Market struct {
	now    func() time.Time
	series []*series          // in universe order
	byID   map[string]*series // orderbook id → series

	pinMu sync.RWMutex
	pins  map[string]float64 // orderbook id → pinned, rounded last price
}

// New returns a simulated market reading the time from now (nil means time.Now).
func New(now func() time.Time) *Market {
	if now == nil {
		now = time.Now
	}
	m := &Market{now: now, byID: make(map[string]*series, len(universe)), pins: map[string]float64{}}
	for i := range universe {
		s := newSeries(&universe[i])
		m.series = append(m.series, s)
		m.byID[universe[i].orderbookID()] = s
	}
	return m
}

func (m *Market) lookup(orderbookID string) (*series, error) {
	s, ok := m.byID[orderbookID]
	if !ok {
		return nil, fmt.Errorf("simmarket: %q: %w", orderbookID, domain.ErrInstrumentNotFound)
	}
	return s, nil
}

// nowMinute returns the current time and its minute index since Epoch (floored; negative before Epoch).
func (m *Market) nowMinute() (time.Time, int64) {
	now := m.now().UTC()
	d := now.Sub(Epoch)
	idx := int64(d / time.Minute)
	if d < 0 && d%time.Minute != 0 {
		idx--
	}
	return now, idx
}

// lastAndChange returns the (pinned or path) rounded last price and the change against 24 hours earlier.
func (m *Market) lastAndChange(s *series, minute int64) (last, change float64) {
	m.pinMu.RLock()
	pin, pinned := m.pins[s.inst.orderbookID()]
	m.pinMu.RUnlock()
	if pinned {
		last = pin
	} else {
		last = domain.RoundPrice(s.price(minute))
	}
	ref := s.price(minute - minutesPerDay)
	change = domain.RoundPrice((last - ref) / ref * 100)
	return last, change
}

func (m *Market) summary(s *series, minute int64) domain.InstrumentSummary {
	last, change := m.lastAndChange(s, minute)
	return domain.InstrumentSummary{
		OrderbookID:   s.inst.orderbookID(),
		Symbol:        s.inst.symbol,
		Name:          s.inst.name,
		Currency:      currency,
		Market:        market,
		LastPrice:     last,
		ChangePercent: change,
	}
}

// Search matches query case-insensitively against symbol, name and orderbook id. Exact symbol or id
// matches come first, then name prefix matches, then the rest, each group ordered by name.
func (m *Market) Search(_ context.Context, query string) ([]domain.InstrumentSummary, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	type hit struct {
		s    *series
		rank int
	}
	var hits []hit
	for _, s := range m.series {
		sym, name, id := strings.ToLower(s.inst.symbol), strings.ToLower(s.inst.name), strings.ToLower(s.inst.orderbookID())
		switch {
		case q == sym || q == id:
			hits = append(hits, hit{s, 0})
		case strings.HasPrefix(name, q):
			hits = append(hits, hit{s, 1})
		case strings.Contains(sym, q) || strings.Contains(name, q) || strings.Contains(id, q):
			hits = append(hits, hit{s, 2})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		return hits[i].s.inst.name < hits[j].s.inst.name
	})
	if len(hits) > maxSearchResults {
		hits = hits[:maxSearchResults]
	}
	_, minute := m.nowMinute()
	out := make([]domain.InstrumentSummary, 0, len(hits))
	for _, h := range hits {
		out = append(out, m.summary(h.s, minute))
	}
	return out, nil
}

// Quote returns the last price (pinned or simulated) with a 20 bps spread around it.
func (m *Market) Quote(_ context.Context, orderbookID string) (domain.Quote, error) {
	s, err := m.lookup(orderbookID)
	if err != nil {
		return domain.Quote{}, err
	}
	now, minute := m.nowMinute()
	last, change := m.lastAndChange(s, minute)
	return domain.Quote{
		OrderbookID:   s.inst.orderbookID(),
		Symbol:        s.inst.symbol,
		Last:          last,
		Bid:           domain.RoundPrice(last * 0.999),
		Ask:           domain.RoundPrice(last * 1.001),
		ChangePercent: change,
		Currency:      currency,
		UpdatedAt:     now.Truncate(time.Minute),
	}, nil
}

// Details returns the company profile with key indicators derived from the current last price.
func (m *Market) Details(_ context.Context, orderbookID string) (domain.InstrumentDetails, error) {
	s, err := m.lookup(orderbookID)
	if err != nil {
		return domain.InstrumentDetails{}, err
	}
	_, minute := m.nowMinute()
	sum := m.summary(s, minute)
	in := s.inst

	var pe *float64
	if in.eps > 0 {
		pe = ptr(domain.RoundPrice(sum.LastPrice / in.eps))
	}
	return domain.InstrumentDetails{
		OrderbookID:   sum.OrderbookID,
		Symbol:        sum.Symbol,
		Name:          sum.Name,
		Currency:      sum.Currency,
		Market:        sum.Market,
		Sector:        in.sector,
		Description:   in.description,
		LastPrice:     sum.LastPrice,
		ChangePercent: sum.ChangePercent,
		KeyIndicators: domain.KeyIndicators{
			PERatio:          pe,
			DividendYieldPct: ptr(domain.RoundPrice(in.dps / sum.LastPrice * 100)),
			MarketCap:        ptr(domain.RoundPrice(in.shares * sum.LastPrice)),
		},
	}, nil
}

// periodSpec is the bar layout of a candle period: count bars of either barMinutes or barDays.
type periodSpec struct {
	count      int64
	barMinutes int64
	barDays    int64
}

var periodSpecs = map[domain.CandlePeriod]periodSpec{
	domain.Period1D: {count: 96, barMinutes: 15},
	domain.Period1W: {count: 168, barMinutes: 60},
	domain.Period1M: {count: 30, barDays: 1},
	domain.Period3M: {count: 90, barDays: 1},
	domain.Period1Y: {count: 52, barDays: 7},
}

// Candles returns the bars of period ending with the current, partial bar. Bars are aligned to
// their size since Epoch, bars before Epoch are omitted, and pinned prices are ignored.
func (m *Market) Candles(_ context.Context, orderbookID string, period domain.CandlePeriod) ([]domain.Candle, error) {
	s, err := m.lookup(orderbookID)
	if err != nil {
		return nil, err
	}
	spec, ok := periodSpecs[period]
	if !ok {
		return nil, &domain.ValidationError{Msg: "period must be one of 1d, 1w, 1m, 3m, 1y"}
	}
	_, minute := m.nowMinute()
	candles := []domain.Candle{}
	if minute < 0 {
		return candles, nil
	}

	var bars []bar
	var first, size int64 // first bar start and bar size, in minutes
	if spec.barMinutes > 0 {
		size = spec.barMinutes
		first = max(minute/size*size-(spec.count-1)*size, 0)
		bars = s.minuteBars(first, minute, size)
	} else {
		nowDay := minute / minutesPerDay
		firstDay := max(nowDay/spec.barDays*spec.barDays-(spec.count-1)*spec.barDays, 0)
		size = spec.barDays * minutesPerDay
		first = firstDay * minutesPerDay
		bars = s.dayBars(firstDay, minute, spec.barDays)
	}
	for i, b := range bars {
		candles = append(candles, domain.Candle{
			T: Epoch.Add(time.Duration(first+int64(i)*size) * time.Minute),
			O: domain.RoundPrice(b.open),
			H: domain.RoundPrice(b.high),
			L: domain.RoundPrice(b.low),
			C: domain.RoundPrice(b.close),
			V: b.volume,
		})
	}
	return candles, nil
}

// SetPrice pins the last price of orderbookID to RoundPrice(price) until ClearPrice.
func (m *Market) SetPrice(_ context.Context, orderbookID string, price float64) error {
	if _, err := m.lookup(orderbookID); err != nil {
		return err
	}
	rounded := domain.RoundPrice(price)
	if !(price > 0) || !(rounded > 0) || math.IsInf(price, 0) {
		return &domain.ValidationError{Msg: "price must be greater than 0"}
	}
	m.pinMu.Lock()
	m.pins[orderbookID] = rounded
	m.pinMu.Unlock()
	return nil
}

// ClearPrice removes the pin of orderbookID, if any.
func (m *Market) ClearPrice(_ context.Context, orderbookID string) error {
	if _, err := m.lookup(orderbookID); err != nil {
		return err
	}
	m.pinMu.Lock()
	delete(m.pins, orderbookID)
	m.pinMu.Unlock()
	return nil
}

func ptr(v float64) *float64 { return &v }
