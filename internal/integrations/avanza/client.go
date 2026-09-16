package avanza

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const (
	baseURL    = "https://www.avanza.se"
	searchPath = "/_api/search/filtered-search"
)



type Client struct {
	httpClient *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &Client{
		httpClient: httpClient,
	}
}

func (c *Client) do(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	)
	req.Header.Set("Referer", "https://www.avanza.se/")
	req.Header.Set("Origin", "https://www.avanza.se")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Avanza: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		return nil, fmt.Errorf(
			"avanza returned HTTP %d: %s",
			resp.StatusCode,
			string(responseBody),
		)
	}

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Avanza response: %w", err)
	}

	return result, nil
}

func (c *Client) Search(
	ctx context.Context,
	query string,
) ([]byte, error) {
	payload := SearchRequest{
		Query: query,
		SearchFilter: SearchFilter{
			Types: []string{},
		},
		ScreenSize:     "DESKTOP",
		OriginPath:     "/start",
		OriginPlatform: "PWA",
		SearchSessionID: uuid.NewString(),
		Pagination: Pagination{
			From: 0,
			Size: 30,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal search payload: %w", err)
	}

	return c.do(
		ctx,
		http.MethodPost,
		searchPath,
		body,
	)
}

func (c *Client) GetPriceChart(
	ctx context.Context,
	orderbookID string,
	timePeriod string,
) ([]byte, error) {
	path := fmt.Sprintf(
		"/_api/price-chart/stock/%s/compare/1002994?timePeriod=%s",
		url.PathEscape(orderbookID),
		url.QueryEscape(timePeriod),
	)

	return c.do(
		ctx,
		http.MethodGet,
		path,
		nil,
	)
}


func (c *Client) GetStockDetails(
	ctx context.Context,
	orderbookID string,
) ([]byte, error) {
	path := fmt.Sprintf(
		"/_api/stock-guide/%s/details",
		url.PathEscape(orderbookID),
	)

	return c.do(
		ctx,
		http.MethodGet,
		path,
		nil,
	)
}

func (c *Client) GetStockQuote(
	ctx context.Context,
	orderbookID string,
) ([]byte, error) {
	path := fmt.Sprintf(
		"/_api/stock-guide/%s/quote",
		url.PathEscape(orderbookID),
	)

	return c.do(
		ctx,
		http.MethodGet,
		path,
		nil,
	)
}