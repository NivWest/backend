package avanza

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"backend/internal/domain"

	"github.com/google/uuid"
)

var _ domain.MarketDataProvider = (*MarketProvider)(nil)

const (
	defaultCurrency = "SEK"
	maxSearchHits   = 20
	searchPageSize  = 30
	searchTypeStock = "STOCK"
)

// chartTimePeriods maps a domain period to Avanza's price-chart timePeriod parameter.
var chartTimePeriods = map[domain.CandlePeriod]string{
	domain.Period1D: "one_day",
	domain.Period1W: "one_week",
	domain.Period1M: "one_month",
	domain.Period3M: "three_months",
	domain.Period1Y: "one_year",
}

// MarketProvider is the live domain.MarketDataProvider backed by Avanza's public web API.
type MarketProvider struct {
	httpClient *http.Client
	baseURL    string
}

// NewMarketProvider returns a provider that sends every request to baseURL + path.
// A nil httpClient gets a 10 s timeout; an empty baseURL means https://www.avanza.se.
func NewMarketProvider(httpClient *http.Client, baseURL string) *MarketProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &MarketProvider{
		httpClient: defaultHTTPClient(httpClient),
		baseURL:    baseURL,
	}
}

// Search returns at most 20 stock hits for query, in Avanza's order.
func (p *MarketProvider) Search(ctx context.Context, query string) ([]domain.InstrumentSummary, error) {
	const op = "search"

	payload, err := json.Marshal(searchRequestBody{
		Query:           query,
		SearchFilter:    searchFilterBody{Types: []string{}},
		ScreenSize:      "DESKTOP",
		OriginPath:      "/start",
		OriginPlatform:  "PWA",
		SearchSessionID: uuid.NewString(),
		Pagination:      searchPagingParams{From: 0, Size: searchPageSize},
	})
	if err != nil {
		return nil, unavailable(op, fmt.Errorf("marshal search payload: %w", err))
	}

	body, err := do(ctx, p.httpClient, p.baseURL, http.MethodPost, searchPath, payload)
	if err != nil {
		return nil, unavailable(op, err)
	}

	items := []domain.InstrumentSummary{}
	if isEmptyPayload(body) {
		return items, nil
	}

	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, unavailable(op, fmt.Errorf("decode response: %w", err))
	}

	for _, hit := range resp.Hits {
		if len(items) == maxSearchHits {
			break
		}
		if hit.Type != searchTypeStock || hit.OrderBookID == "" {
			continue
		}
		items = append(items, mapSearchHit(hit))
	}

	return items, nil
}

func mapSearchHit(hit searchHit) domain.InstrumentSummary {
	name, symbol := splitTitle(hit.Title)
	if symbol == "" {
		symbol = strings.TrimSpace(hit.TickerSymbol)
	}

	item := domain.InstrumentSummary{
		OrderbookID: hit.OrderBookID,
		Symbol:      symbol,
		Name:        name,
		Currency:    defaultCurrency,
		Market:      hit.MarketPlaceName,
	}
	if hit.Price != nil {
		item.Currency = currencyOrDefault(hit.Price.Currency)
		item.LastPrice = domain.RoundPrice(hit.Price.Last.Value)
		item.ChangePercent = domain.RoundPrice(hit.Price.TodayChangePercent.Value)
	}

	return item
}

// splitTitle turns "Volvo B (VOLV B)" into ("Volvo B", "VOLV B"). A title without a trailing
// parenthesised ticker is returned unchanged with an empty symbol.
func splitTitle(title string) (name, symbol string) {
	title = strings.TrimSpace(title)
	if !strings.HasSuffix(title, ")") {
		return title, ""
	}

	open := strings.LastIndex(title, "(")
	if open <= 0 {
		return title, ""
	}

	ticker := strings.TrimSpace(title[open+1 : len(title)-1])
	name = strings.TrimSpace(title[:open])
	if ticker == "" || name == "" || strings.ContainsAny(ticker, "()") {
		return title, ""
	}

	return name, ticker
}

// Quote returns the latest price from /_api/stock-guide/{id}/quote. Avanza answers an unknown
// id with an empty 200, which maps to ErrInstrumentNotFound.
func (p *MarketProvider) Quote(ctx context.Context, orderbookID string) (domain.Quote, error) {
	q, err := p.fetchQuote(ctx, orderbookID)
	if err != nil {
		return domain.Quote{}, err
	}

	return mapQuote(orderbookID, q), nil
}

func (p *MarketProvider) fetchQuote(ctx context.Context, orderbookID string) (quoteResponse, error) {
	var q quoteResponse
	path := fmt.Sprintf("/_api/stock-guide/%s/quote", url.PathEscape(orderbookID))
	if err := p.getJSON(ctx, "quote", orderbookID, path, &q); err != nil {
		return quoteResponse{}, err
	}
	if q.empty() {
		return quoteResponse{}, notFound("quote", orderbookID)
	}

	return q, nil
}

func mapQuote(orderbookID string, q quoteResponse) domain.Quote {
	last := deref(q.Last)
	bid, ask := last, last
	if q.Buy != nil && *q.Buy > 0 {
		bid = *q.Buy
	}
	if q.Sell != nil && *q.Sell > 0 {
		ask = *q.Sell
	}

	return domain.Quote{
		OrderbookID:   orderbookID,
		Symbol:        "",
		Last:          domain.RoundPrice(last),
		Bid:           domain.RoundPrice(bid),
		Ask:           domain.RoundPrice(ask),
		ChangePercent: domain.RoundPrice(deref(q.ChangePercent)),
		Currency:      defaultCurrency,
		UpdatedAt:     quoteTime(q),
	}
}

func quoteTime(q quoteResponse) time.Time {
	switch {
	case q.Updated != nil && *q.Updated > 0:
		return time.UnixMilli(*q.Updated).UTC()
	case q.TimeOfLast != nil && *q.TimeOfLast > 0:
		return time.UnixMilli(*q.TimeOfLast).UTC()
	default:
		return time.Now().UTC()
	}
}

// Details combines the market guide (name, ticker, listing, latest quote) with the stock guide
// details (description, sector, key indicators). The quote endpoint is asked only when neither has a price.
func (p *MarketProvider) Details(ctx context.Context, orderbookID string) (domain.InstrumentDetails, error) {
	// The market guide goes first: it answers an unknown id with 404, where the details endpoint answers 500.
	var guide marketGuideResponse
	guidePath := fmt.Sprintf("/_api/market-guide/stock/%s", url.PathEscape(orderbookID))
	if err := p.getJSON(ctx, "market guide", orderbookID, guidePath, &guide); err != nil {
		return domain.InstrumentDetails{}, err
	}
	if guide.Name == "" && guide.OrderbookID == "" {
		return domain.InstrumentDetails{}, notFound("market guide", orderbookID)
	}

	var details detailsResponse
	detailsPath := fmt.Sprintf("/_api/stock-guide/%s/details", url.PathEscape(orderbookID))
	if err := p.getJSON(ctx, "details", orderbookID, detailsPath, &details); err != nil {
		return domain.InstrumentDetails{}, err
	}

	out := domain.InstrumentDetails{
		OrderbookID:   orderbookID,
		Symbol:        strings.TrimSpace(guide.Listing.TickerSymbol),
		Name:          firstNonEmpty(details.Name, guide.Name),
		Currency:      currencyOrDefault(guide.Listing.Currency),
		Market:        guide.Listing.MarketPlaceName,
		Sector:        firstNonEmpty(firstSector(details.Sectors), firstSector(guide.Sectors)),
		KeyIndicators: mapKeyIndicators(details.KeyIndicators, guide.KeyIndicators),
	}
	if details.Company != nil {
		out.Description = strings.TrimSpace(details.Company.Description)
	}

	quote, err := p.detailsQuote(ctx, orderbookID, details.Quote, guide.Quote)
	if err != nil {
		return domain.InstrumentDetails{}, err
	}
	out.LastPrice = domain.RoundPrice(deref(quote.Last))
	out.ChangePercent = domain.RoundPrice(deref(quote.ChangePercent))

	return out, nil
}

func (p *MarketProvider) detailsQuote(
	ctx context.Context,
	orderbookID string,
	candidates ...*quoteResponse,
) (quoteResponse, error) {
	for _, q := range candidates {
		if q != nil && q.Last != nil {
			return *q, nil
		}
	}

	return p.fetchQuote(ctx, orderbookID)
}

// mapKeyIndicators takes each field from the first source that has it.
// Avanza sends the direct yield as a fraction (0.0136), the domain wants percent (1.36).
func mapKeyIndicators(sources ...*keyIndicatorsPayload) domain.KeyIndicators {
	var out domain.KeyIndicators
	for _, k := range sources {
		if k == nil {
			continue
		}
		if out.PERatio == nil && k.PriceEarningsRatio != nil {
			out.PERatio = ptr(domain.RoundPrice(*k.PriceEarningsRatio))
		}
		if out.DividendYieldPct == nil {
			if y := firstNonNil(k.DirectYield, k.OrdinaryDirectYield); y != nil {
				out.DividendYieldPct = ptr(domain.RoundPrice(*y * 100))
			}
		}
		if out.MarketCap == nil && k.MarketCapital != nil && k.MarketCapital.Value != nil {
			out.MarketCap = ptr(*k.MarketCapital.Value)
		}
	}

	return out
}

// Candles returns the price chart for period in ascending time, without duplicate or zero-price bars.
func (p *MarketProvider) Candles(
	ctx context.Context,
	orderbookID string,
	period domain.CandlePeriod,
) ([]domain.Candle, error) {
	timePeriod, ok := chartTimePeriods[period]
	if !ok {
		return nil, &domain.ValidationError{Msg: "period must be one of 1d, 1w, 1m, 3m, 1y"}
	}

	var chart chartResponse
	path := fmt.Sprintf(
		"/_api/price-chart/stock/%s?timePeriod=%s",
		url.PathEscape(orderbookID),
		url.QueryEscape(timePeriod),
	)
	if err := p.getJSON(ctx, "candles", orderbookID, path, &chart); err != nil {
		return nil, err
	}

	return mapCandles(chart.OHLC), nil
}

func mapCandles(points []chartPoint) []domain.Candle {
	sorted := make([]chartPoint, 0, len(points))
	for _, pt := range points {
		if pt.Timestamp > 0 && pt.Close != nil && *pt.Close > 0 {
			sorted = append(sorted, pt)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Timestamp < sorted[j].Timestamp })

	candles := make([]domain.Candle, 0, len(sorted))
	for i, pt := range sorted {
		if i > 0 && pt.Timestamp == sorted[i-1].Timestamp {
			continue
		}

		c := *pt.Close
		o := positiveOr(pt.Open, c)
		h := math.Max(positiveOr(pt.High, c), math.Max(o, c))
		l := math.Min(positiveOr(pt.Low, c), math.Min(o, c))

		candles = append(candles, domain.Candle{
			T: time.UnixMilli(pt.Timestamp).UTC(),
			O: domain.RoundPrice(o),
			H: domain.RoundPrice(h),
			L: domain.RoundPrice(l),
			C: domain.RoundPrice(c),
			V: int64(math.Round(deref(pt.TotalVolumeTraded))),
		})
	}

	return candles
}

// getJSON GETs path and decodes it into out. 404 and an empty body map to ErrInstrumentNotFound;
// everything else that fails wraps ErrMarketDataUnavailable.
func (p *MarketProvider) getJSON(ctx context.Context, op, orderbookID, path string, out any) error {
	body, err := do(ctx, p.httpClient, p.baseURL, http.MethodGet, path, nil)
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return notFound(op, orderbookID)
		}
		return unavailable(op, err)
	}

	if isEmptyPayload(body) {
		return notFound(op, orderbookID)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return unavailable(op, fmt.Errorf("decode response: %w", err))
	}

	return nil
}

func notFound(op, orderbookID string) error {
	return fmt.Errorf("avanza %s %q: %w", op, truncate(orderbookID, 64), domain.ErrInstrumentNotFound)
}

func unavailable(op string, cause error) error {
	return fmt.Errorf("avanza %s: %w: %w", op, domain.ErrMarketDataUnavailable, cause)
}

func isEmptyPayload(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) == 0 ||
		bytes.Equal(trimmed, []byte("null")) ||
		bytes.Equal(trimmed, []byte("{}"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func currencyOrDefault(c string) string {
	if c = strings.TrimSpace(c); c != "" {
		return c
	}
	return defaultCurrency
}

func firstSector(sectors []sectorDTO) string {
	for _, s := range sectors {
		if name := strings.TrimSpace(s.SectorName); name != "" {
			return name
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func firstNonNil(values ...*float64) *float64 {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func positiveOr(v *float64, fallback float64) float64 {
	if v != nil && *v > 0 {
		return *v
	}
	return fallback
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func ptr(v float64) *float64 { return &v }
