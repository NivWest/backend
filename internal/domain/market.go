package domain

import (
	"context"
	"errors"
	"math"
	"time"
)

// Market data (story NW-03). Every provider maps its upstream payloads into these DTOs, so handlers
// and services never see Avanza's (or anyone else's) JSON. Shapes: api/openapi.yaml#/components/schemas.

// Market data error codes, returned as ErrorResponse.Code.
const (
	CodeInstrumentNotFound    = "INSTRUMENT_NOT_FOUND"
	CodeMarketDataUnavailable = "MARKET_DATA_UNAVAILABLE"
)

var (
	// ErrInstrumentNotFound means the provider does not know the orderbook id. Handlers map it to 404 INSTRUMENT_NOT_FOUND.
	ErrInstrumentNotFound = errors.New("instrument not found")
	// ErrMarketDataUnavailable means the upstream failed (network, timeout, non-2xx, unparsable payload).
	// Providers wrap it with %w so the cause stays in the error chain. Handlers map it to 502 MARKET_DATA_UNAVAILABLE.
	ErrMarketDataUnavailable = errors.New("market data unavailable")
)

// ValidationError is a rejected input (blank query, unknown period, non-positive price).
// Handlers detect it with errors.As and answer 400 VALIDATION_ERROR with Msg as the error text.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// InstrumentSummary is one search hit (#/components/schemas/InstrumentSummary).
type InstrumentSummary struct {
	OrderbookID   string  `json:"orderbook_id"`
	Symbol        string  `json:"symbol"`
	Name          string  `json:"name"`
	Currency      string  `json:"currency"`
	Market        string  `json:"market"`
	LastPrice     float64 `json:"last_price"`
	ChangePercent float64 `json:"change_percent"`
}

// InstrumentSearchResponse is the body of GET /market/instruments (#/components/schemas/InstrumentSearchResponse).
// Items is never nil (an empty search serializes as []); NextCursor is always nil until search is paginated.
type InstrumentSearchResponse struct {
	Items      []InstrumentSummary `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// Quote is the latest price of one instrument (#/components/schemas/Quote).
// Last, Bid and Ask are rounded half-up to 2 decimals (RoundPrice); UpdatedAt is UTC.
type Quote struct {
	OrderbookID   string    `json:"orderbook_id"`
	Symbol        string    `json:"symbol"`
	Last          float64   `json:"last"`
	Bid           float64   `json:"bid"`
	Ask           float64   `json:"ask"`
	ChangePercent float64   `json:"change_percent"`
	Currency      string    `json:"currency"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// KeyIndicators are the fundamentals shown on the instrument page (#/components/schemas/KeyIndicators).
// A nil field means the provider has no value (e.g. no P/E for a loss-making company); it serializes as null.
type KeyIndicators struct {
	PERatio          *float64 `json:"pe_ratio"`
	DividendYieldPct *float64 `json:"dividend_yield_pct"`
	// MarketCap is in the instrument's currency.
	MarketCap *float64 `json:"market_cap"`
}

// InstrumentDetails is the body of GET /market/instruments/{orderbook_id} (#/components/schemas/InstrumentDetails).
type InstrumentDetails struct {
	OrderbookID   string        `json:"orderbook_id"`
	Symbol        string        `json:"symbol"`
	Name          string        `json:"name"`
	Currency      string        `json:"currency"`
	Market        string        `json:"market"`
	Sector        string        `json:"sector"`
	Description   string        `json:"description"`
	LastPrice     float64       `json:"last_price"`
	ChangePercent float64       `json:"change_percent"`
	KeyIndicators KeyIndicators `json:"key_indicators"`
}

// Candle is one OHLCV bar (#/components/schemas/Candle). T is the UTC start of the bar.
type Candle struct {
	T time.Time `json:"t"`
	O float64   `json:"o"`
	H float64   `json:"h"`
	L float64   `json:"l"`
	C float64   `json:"c"`
	V int64     `json:"v"`
}

// CandlesResponse is the body of GET /market/instruments/{orderbook_id}/candles (#/components/schemas/CandlesResponse).
// Candles are in strictly ascending T and never nil.
type CandlesResponse struct {
	OrderbookID string       `json:"orderbook_id"`
	Period      CandlePeriod `json:"period"`
	Candles     []Candle     `json:"candles"`
}

// CandlePeriod is the chart window requested with ?period=.
type CandlePeriod string

// Candle periods. The window ends now; the bar size is a guideline every provider follows as closely as its upstream allows:
//
//	1d → last 24 hours,  15-minute bars (96)
//	1w → last 7 days,    1-hour bars    (168)
//	1m → last 30 days,   1-day bars     (30)
//	3m → last 90 days,   1-day bars     (90)
//	1y → last 365 days,  1-week bars    (52)
const (
	Period1D CandlePeriod = "1d"
	Period1W CandlePeriod = "1w"
	Period1M CandlePeriod = "1m"
	Period3M CandlePeriod = "3m"
	Period1Y CandlePeriod = "1y"
)

// CandlePeriods lists the accepted periods in display order.
var CandlePeriods = []CandlePeriod{Period1D, Period1W, Period1M, Period3M, Period1Y}

// ParseCandlePeriod returns the period for s, or a *ValidationError when s is not one of CandlePeriods.
func ParseCandlePeriod(s string) (CandlePeriod, error) {
	for _, p := range CandlePeriods {
		if string(p) == s {
			return p, nil
		}
	}
	return "", &ValidationError{Msg: "period must be one of 1d, 1w, 1m, 3m, 1y"}
}

// RoundPrice rounds v half-up to 2 decimals. The epsilon absorbs float noise such as 1.005*100 = 100.49999….
func RoundPrice(v float64) float64 {
	return math.Floor(v*100+0.5+1e-9) / 100
}

// MarketDataProvider is the market data port. Implementations: internal/integrations/avanza (live)
// and internal/integrations/simmarket (deterministic). Selected by MARKET_DATA_PROVIDER in internal/app.
// Unknown ids return ErrInstrumentNotFound; upstream failures wrap ErrMarketDataUnavailable.
// Inputs are already validated by the service (non-blank query and id, valid period).
type MarketDataProvider interface {
	// Search returns instruments whose symbol, name or orderbook id matches query (case-insensitive), best match first, at most 20.
	// No match returns an empty, non-nil slice.
	Search(ctx context.Context, query string) ([]InstrumentSummary, error)
	Quote(ctx context.Context, orderbookID string) (Quote, error)
	Details(ctx context.Context, orderbookID string) (InstrumentDetails, error)
	Candles(ctx context.Context, orderbookID string, period CandlePeriod) ([]Candle, error)
}

// MarketSimControl pins prices on the simulated market. Dev/test only: mounted at /sim when
// MARKET_DATA_PROVIDER=simulated and MARKET_SIM_CONTROL_ENABLED=true. Implemented by simmarket.
type MarketSimControl interface {
	// SetPrice makes Quote, Search and Details report exactly RoundPrice(price) as the last price until ClearPrice.
	// Unknown ids return ErrInstrumentNotFound; price <= 0 returns a *ValidationError.
	SetPrice(ctx context.Context, orderbookID string, price float64) error
	// ClearPrice removes the pin (a no-op when there is none) so the price follows the simulated path again.
	// Unknown ids return ErrInstrumentNotFound.
	ClearPrice(ctx context.Context, orderbookID string) error
}

// SimPriceRequest is the body of PUT /sim/instruments/{orderbook_id}/price (#/components/schemas/SimPriceRequest).
type SimPriceRequest struct {
	Price float64 `json:"price"`
}
