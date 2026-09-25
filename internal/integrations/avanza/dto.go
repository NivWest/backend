package avanza

type SearchRequest struct {
	Query           string       `json:"query"`
	SearchFilter    SearchFilter `json:"searchFilter"`
	ScreenSize      string       `json:"screenSize"`
	OriginPath      string       `json:"originPath"`
	OriginPlatform  string       `json:"originPlatform"`
	SearchSessionID string       `json:"searchSessionId"`
	Pagination      Pagination   `json:"pagination"`
}

type SearchFilter struct {
	Types []string `json:"types"`
}

type Pagination struct {
	From int `json:"from"`
	Size int `json:"size"`
}
