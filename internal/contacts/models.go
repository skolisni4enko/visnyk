package contacts

import "visnyk/internal/cascade"

// InvalidEntry describes a row that failed normalization.
type InvalidEntry struct {
	Row   int    `json:"row"`
	Raw   string `json:"raw"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// ParseResult is returned to Wails frontend.
type ParseResult struct {
	Contacts   []cascade.Contact `json:"contacts"`
	Invalid    []InvalidEntry    `json:"invalid"`
	Duplicates int               `json:"duplicates"`
	Total      int               `json:"total"`
}
