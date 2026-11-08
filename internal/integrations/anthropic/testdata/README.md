# Anthropic adapter fixtures

Response bodies of `POST /v1/messages` (beta) that `llm_test.go` replays from an `httptest` server through
`option.WithBaseURL`. They follow the documented Messages API wire format (`content` blocks, `stop_reason`,
`usage` with cache tokens and `iterations`, the SSE event sequence of a streamed message). `go test` never
calls the real API. To refresh one, run the request with a real key and paste the response body here, with
ids and any user data replaced.

| File | Case |
|---|---|
| `brief_ok.json` | Structured-output brief, `end_turn`, with a cache read |
| `refusal.json` | `stop_reason: refusal` with `stop_details` |
| `max_tokens.json` | Answer cut off at `max_tokens` |
| `fallback.json` | The server-side fallback answered (`fallback` block, `fallback_message` iteration) |
| `overloaded.json` | A 529 `overloaded_error` body |
| `stream_ok.sse` | A streamed text answer |
