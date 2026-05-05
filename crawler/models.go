package crawler

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Options struct {
	URL         string
	Depth       int32
	HTTPClient  *http.Client
	Delay       time.Duration
	RPS         int
	Retries     int
	UserAgent   string
	Timeout     time.Duration
	Concurrency int
	IndentJSON  bool
}

type Report struct {
	RootURL     string `json:"root_url"`
	Depth       int32  `json:"depth"`
	GeneratedAt string `json:"generated_at"`
	Pages       []Page `json:"pages"`
}

type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HttpStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	BrokenLinks  []BrokenLink `json:"broken_links,omitempty"`
	SEO          SEO          `json:"seo"`
	Assets       []Asset      `json:"assets,omitempty"`
	DiscoveredAt string       `json:"discovered_at"`
}

type Asset struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
	Error      string `json:"error,omitempty"`
}

type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Link struct {
	URL              string  `json:"url"`
	StatusCode       *int    `json:"status_code,omitempty"`
	Error            *string `json:"error,omitempty"`
	ParentURL        string  `json:"parent_url,omitempty"`
	ParentStatusCode int     `json:"parent_http_status,omitempty"`
	ParentStatus     string  `json:"parent_status,omitempty"`
	SEO              *SEO    `json:"seo,omitempty"`
	Depth            int     `json:"depth"`
	Assets           []Asset `json:"assets,omitempty"`
}

type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title,omitempty"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

var (
	visited       = make(map[string]struct{})
	visitedMu     sync.RWMutex
	globalLimiter *rate.Limiter
	assetsCache   = make(map[string]Asset)
	assetsCacheMu sync.RWMutex
)

type Job struct {
	URL              string
	ParentURL        string
	ParentStatusCode int
	ParentStatus     string
	Depth            int
}
