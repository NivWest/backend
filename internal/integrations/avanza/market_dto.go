package avanza

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Upstream payloads read by MarketProvider. Only the fields the domain needs are declared;
// recorded examples live in testdata/.

// searchRequestBody is the POST body of /_api/search/filtered-search.
type searchRequestBody struct {
	Query           string             `json:"query"`
	SearchFilter    searchFilterBody   `json:"searchFilter"`
	ScreenSize      string             `json:"screenSize"`
	OriginPath      string             `json:"originPath"`
	OriginPlatform  string             `json:"originPlatform"`
	SearchSessionID string             `json:"searchSessionId"`
	Pagination      searchPagingParams `json:"pagination"`
}

type searchFilterBody struct {
	Types []string `json:"types"`
}

type searchPagingParams struct {
	From int `json:"from"`
	Size int `json:"size"`
}

type searchResponse struct {
	Hits []searchHit `json:"hits"`
}

type searchHit struct {
	Type            string       `json:"type"`
	Title           string       `json:"title"`
	OrderBookID     string       `json:"orderBookId"`
	TickerSymbol    string       `json:"tickerSymbol"`
	MarketPlaceName string       `json:"marketPlaceName"`
	Price           *searchPrice `json:"price"`
}

type searchPrice struct {
	Last               flexNumber `json:"last"`
	Currency           string     `json:"currency"`
	TodayChangePercent flexNumber `json:"todayChangePercent"`
}

// quoteResponse is GET /_api/stock-guide/{id}/quote (also embedded in the market guide).
type quoteResponse struct {
	Buy           *float64 `json:"buy"`
	Sell          *float64 `json:"sell"`
	Last          *float64 `json:"last"`
	ChangePercent *float64 `json:"changePercent"`
	TimeOfLast    *int64   `json:"timeOfLast"`
	Updated       *int64   `json:"updated"`
}

func (q quoteResponse) empty() bool {
	return q.Last == nil && q.Buy == nil && q.Sell == nil && q.Updated == nil && q.TimeOfLast == nil
}

// marketGuideResponse is GET /_api/market-guide/stock/{id}: names, listing and the latest quote.
type marketGuideResponse struct {
	OrderbookID   string                `json:"orderbookId"`
	Name          string                `json:"name"`
	Sectors       []sectorDTO           `json:"sectors"`
	Listing       marketGuideListing    `json:"listing"`
	KeyIndicators *keyIndicatorsPayload `json:"keyIndicators"`
	Quote         *quoteResponse        `json:"quote"`
}

type marketGuideListing struct {
	TickerSymbol    string `json:"tickerSymbol"`
	Currency        string `json:"currency"`
	MarketPlaceName string `json:"marketPlaceName"`
}

// detailsResponse is GET /_api/stock-guide/{id}/details.
type detailsResponse struct {
	Name          string                `json:"name"`
	Sectors       []sectorDTO           `json:"sectors"`
	Company       *detailsCompany       `json:"company"`
	KeyIndicators *keyIndicatorsPayload `json:"keyIndicators"`
	Quote         *quoteResponse        `json:"quote"`
}

type detailsCompany struct {
	Description string `json:"description"`
}

type sectorDTO struct {
	SectorName string `json:"sectorName"`
}

type keyIndicatorsPayload struct {
	PriceEarningsRatio  *float64     `json:"priceEarningsRatio"`
	DirectYield         *float64     `json:"directYield"`
	OrdinaryDirectYield *float64     `json:"ordinaryDirectYield"`
	MarketCapital       *moneyAmount `json:"marketCapital"`
}

type moneyAmount struct {
	Value *float64 `json:"value"`
}

// chartResponse is GET /_api/price-chart/stock/{id}?timePeriod=….
type chartResponse struct {
	OHLC []chartPoint `json:"ohlc"`
}

type chartPoint struct {
	Timestamp         int64    `json:"timestamp"`
	Open              *float64 `json:"open"`
	High              *float64 `json:"high"`
	Low               *float64 `json:"low"`
	Close             *float64 `json:"close"`
	TotalVolumeTraded *float64 `json:"totalVolumeTraded"`
}

// flexNumber decodes a JSON number or a Swedish-formatted string such as "1,2", "−24,23" (U+2212 minus)
// or "1 234,50". null, "" and "-" decode as absent.
type flexNumber struct {
	Value float64
	Valid bool
}

func (n *flexNumber) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*n = flexNumber{}
		return nil
	}

	if data[0] != '"' {
		var v float64
		if err := json.Unmarshal(data, &v); err != nil {
			return fmt.Errorf("decode number: %w", err)
		}
		*n = flexNumber{Value: v, Valid: true}
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("decode number string: %w", err)
	}

	v, ok, err := parseSwedishNumber(s)
	if err != nil {
		return err
	}
	*n = flexNumber{Value: v, Valid: ok}
	return nil
}

var swedishNumberReplacer = strings.NewReplacer(
	"−", "-", // minus sign
	"–", "-", // en dash
	" ", "", // no-break space (thousands separator)
	" ", "", // narrow no-break space (thousands separator)
	" ", "",
	"%", "",
	",", ".",
)

func parseSwedishNumber(s string) (float64, bool, error) {
	clean := swedishNumberReplacer.Replace(strings.TrimSpace(s))
	if clean == "" || clean == "-" {
		return 0, false, nil
	}
	clean = strings.TrimPrefix(clean, "+")

	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse number %q: %w", truncate(s, 32), err)
	}
	return v, true, nil
}
