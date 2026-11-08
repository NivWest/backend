// Package prompts holds the versioned prompt templates of the AI features (story NW-14). Each template is an
// embedded file <feature>.v<N>.tmpl that defines a "system" block (stable instructions, cacheable: never
// put request data in it) and a "user" block. Bump N for any change to a template: the version is part of
// the brief cache key and is recorded in the usage ledger, so old and new answers stay distinguishable.
//
// Third-party text (instrument names and descriptions) is rendered only inside delimited data tags, as
// JSON produced by encoding/json, which escapes <, > and & (<, >, &). A description can
// therefore never close the tag and smuggle text outside it.
package prompts

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	"backend/internal/domain"
)

//go:embed *.tmpl
var files embed.FS

// Prompt versions, "<feature>.v<N>"; each one names the file <version>.tmpl.
const (
	InstrumentBriefVersion = "instrument_brief.v1"
	InsightsReviewVersion  = "insights_review.v1"
)

// InstrumentDataTag delimits the instrument data in the instrument brief prompt.
const InstrumentDataTag = "instrument_data"

// InsightsDataTag delimits the detector and order data in the insights review prompt.
const InsightsDataTag = "insights_data"

// templates maps each version to its own template set, so every file can define "system" and "user".
var templates = func() map[string]*template.Template {
	names, err := fs.Glob(files, "*.tmpl")
	if err != nil {
		panic(err)
	}
	out := map[string]*template.Template{}
	for _, name := range names {
		version := strings.TrimSuffix(name, ".tmpl")
		out[version] = template.Must(template.New(name).Option("missingkey=error").ParseFS(files, name))
	}
	return out
}()

// Prompt is a rendered prompt.
type Prompt struct {
	Version string
	System  string
	User    string
}

// InstrumentData is the third-party instrument data of the brief prompt; it is rendered as JSON.
// Nil numbers are "not available".
type InstrumentData struct {
	OrderbookID      string   `json:"orderbook_id"`
	Symbol           string   `json:"symbol"`
	Name             string   `json:"name"`
	Sector           string   `json:"sector"`
	Market           string   `json:"market"`
	Currency         string   `json:"currency"`
	Description      string   `json:"description"`
	LastPrice        float64  `json:"last_price"`
	ChangePercent    float64  `json:"change_percent"`
	PERatio          *float64 `json:"pe_ratio"`
	DividendYieldPct *float64 `json:"dividend_yield_pct"`
	MarketCap        *float64 `json:"market_cap"`
}

// InstrumentBrief renders instrument_brief.v1 for d.
func InstrumentBrief(d InstrumentData) (Prompt, error) {
	raw, err := json.MarshalIndent(d, "", "  ") // HTML-escapes <, > and &
	if err != nil {
		return Prompt{}, fmt.Errorf("encode instrument data: %w", err)
	}
	return render(InstrumentBriefVersion, map[string]string{"InstrumentJSON": string(raw)})
}

// InsightsOrderData is the compact order summary passed into the insights review prompt.
type InsightsOrderData struct {
	ID          uint    `json:"id"`
	Side        string  `json:"side"`
	Symbol      string  `json:"symbol"`
	Quantity    string  `json:"quantity"`
	FillPrice   string  `json:"fill_price"`
	RealizedPnL *string `json:"realized_pnl,omitempty"`
	Note        string  `json:"note"`
	CreatedAt   string  `json:"created_at"`
}

// InsightsReviewData is the detector and order history payload rendered inside <insights_data>.
type InsightsReviewData struct {
	Detectors []domain.DetectorResult `json:"detectors"`
	Orders    []InsightsOrderData     `json:"orders"`
}

// InsightsReview renders insights_review.v1 for d.
func InsightsReview(d InsightsReviewData) (Prompt, error) {
	raw, err := json.MarshalIndent(d, "", "  ") // HTML-escapes <, > and &
	if err != nil {
		return Prompt{}, fmt.Errorf("encode insights data: %w", err)
	}
	return render(InsightsReviewVersion, map[string]string{"InsightsJSON": string(raw)})
}

func render(version string, data any) (Prompt, error) {
	set, ok := templates[version]
	if !ok {
		return Prompt{}, fmt.Errorf("prompt %s: no template", version)
	}
	var sys, user bytes.Buffer
	if err := set.ExecuteTemplate(&sys, "system", data); err != nil {
		return Prompt{}, fmt.Errorf("prompt %s system: %w", version, err)
	}
	if err := set.ExecuteTemplate(&user, "user", data); err != nil {
		return Prompt{}, fmt.Errorf("prompt %s user: %w", version, err)
	}
	return Prompt{Version: version, System: strings.TrimSpace(sys.String()), User: strings.TrimSpace(user.String())}, nil
}

// ExtractTagged returns the text between the first <tag> and the next </tag> in s.
func ExtractTagged(s, tag string) (string, bool) {
	open, closing := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, closing)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:j]), true
}
