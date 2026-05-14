package crawler

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Options конфигурация краулера
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

// Report отчет о структуре сайта
type Report struct {
	RootURL     string `json:"root_url"`
	Depth       int32  `json:"depth"`
	GeneratedAt string `json:"generated_at"`
	Pages       []Page `json:"pages"`
}

// Page информация о странице
type Page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HttpStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	BrokenLinks  []BrokenLink `json:"broken_links,omitempty"`
	SEO          SEO          `json:"seo"`
	Assets       []Asset      `json:"assets,omitempty"`
	DiscoveredAt string       `json:"discovered_at"`
	Error        string       `json:"error,omitempty"`
}

// Asset веб-ресурс
type Asset struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
	Error      string `json:"error,omitempty"`
}

// BrokenLink битая ссылка
type BrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Link ссылка в процессе обработки
type Link struct {
	URL              string  `json:"url"`
	StatusCode       *int    `json:"status_code,omitempty"`
	Error            string  `json:"error,omitempty"`
	ParentURL        string  `json:"parent_url,omitempty"`
	ParentStatusCode int     `json:"parent_http_status,omitempty"`
	ParentStatus     string  `json:"parent_status,omitempty"`
	SEO              *SEO    `json:"seo,omitempty"`
	Depth            int     `json:"depth"`
	Assets           []Asset `json:"assets,omitempty"`
}

// SEO метаданные
type SEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

// Job задача для воркера
type Job struct {
	URL              string
	ParentURL        string
	ParentStatusCode int
	ParentStatus     string
	Depth            int
}

// InternalResponse внутренний ответ HTTP
type InternalResponse struct {
	Body       string
	StatusCode int
	Status     string
	Header     http.Header
}

// Константы
const (
	DefaultWorkers     = 4
	JobQueueSize       = 200
	ResultQueueSize    = 200
	MaxRetriesForAsset = 2
	MaxRetryDelay      = 30 * time.Second
)

// Глобальные переменные
var (
	globalLimiter *rate.Limiter
	visited       = make(map[string]struct{})
	visitedMu     sync.RWMutex
	assetsCache   = make(map[string]Asset)
	assetsCacheMu sync.RWMutex
)
