# Avanza test fixtures

Real responses from https://www.avanza.se, recorded with curl on 2026-10-25 and trimmed
(pretty-printed, unrelated sections and most hits/points removed). Field names and value formats are untouched.

| File | Request |
|---|---|
| `search.json` | `POST /_api/search/filtered-search` with query `volvo`: 3 `STOCK` hits plus one `CERTIFICATE` and one `WARRANT` hit |
| `quote.json` | `GET /_api/stock-guide/5247/quote` (Investor B) |
| `details.json` | `GET /_api/stock-guide/5247/details`: `stock`, `sectors`, `company`, `keyIndicators` and two `companyEvents` kept |
| `market_guide.json` | `GET /_api/market-guide/stock/5247` (`marketPlace` removed) |
| `chart.json` | `GET /_api/price-chart/stock/5247?timePeriod=one_month`: first 6 of 189 `ohlc` points |

Upstream behaviour observed while recording, which the adapter relies on:

- Search prices are strings with a decimal comma and U+2212 minus (`"last": "307,70"`, `"todayChangePercent": "−1,12"`).
- `stock-guide/{id}/details` has no instrument name or price, and its `keyIndicators` carries `ordinaryDirectYield`
  (a fraction) but no `directYield`; the market guide has both, plus the name, ticker, listing and quote.
- Unknown numeric ids: `quote` answers 200 with an empty body, `details` answers 500, `market-guide` and `price-chart` answer 404.
