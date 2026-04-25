package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

type Options struct {
	URL        string
	Depth      int32
	HTTPClient *http.Client
	Delay      time.Duration
	RPS        int
	Retries    int
	UserAgent  string
	Workers    int
}

type Root struct {
	RootURL     string `json:"root_url"`
	Depth       int    `json:"depth"`
	GeneratedAt string `json:"generated_at"`
	Pages       []Page `json:"pages"`
}

type Page struct {
	URL          string        `json:"url"`
	Depth        int           `json:"depth"`
	HttpStatus   int           `json:"http_status"`
	Status       string        `json:"status"`
	BrokenLinks  *[]BrokenLink `json:"broken_links,omitempty"`
	Seo          Seo           `json:"seo"`
	DiscoveredAt string        `json:"discovered_at"`
}

type BrokenLink struct {
	URL        string  `json:"url"`
	StatusCode *int    `json:"status_code,omitempty"`
	Error      *string `json:"error,omitempty"`
}

type Link struct {
	URL              string  `json:"url"`
	StatusCode       *int    `json:"status_code,omitempty"`
	Error            *string `json:"error,omitempty"`
	ParentURL        string  `json:"parent_url,omitempty"`
	ParentStatusCode int     `json:"parent_http_status,omitempty"`
	ParentStatus     string  `json:"parent_status,omitempty"`
	Seo              *Seo    `json:"seo,omitempty"`
	Depth            int     `json:"depth"`
}

type Seo struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title,omitempty"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description,omitempty"`
	HasH1          bool   `json:"has_h1"`
}

var (
	visited       = make(map[string]struct{})
	visitedMu     sync.RWMutex
	globalLimiter *rate.Limiter
	lastLogTime   time.Time
	logMu         sync.Mutex
)

type Job struct {
	URL              string
	ParentURL        string
	ParentStatusCode int
	ParentStatus     string
	Depth            int
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Edge/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
}

func getRandomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return userAgents[rng.Intn(len(userAgents))]
}

func getRandomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(255),
		rand.Intn(255),
		rand.Intn(255),
		rand.Intn(255))
}

// setupRateLimiter настраивает глобальный лимитер запросов
func setupRateLimiter(opts Options) {
	if opts.RPS > 0 {
		// burst = 1 - важно! не разрешаем пачку запросов сразу
		globalLimiter = rate.NewLimiter(rate.Limit(opts.RPS), 1)
		fmt.Printf("RPS лимитер: %d запросов/сек (интервал %v)\n", opts.RPS, time.Second/time.Duration(opts.RPS))
	} else if opts.Delay > 0 {
		rps := float64(time.Second) / float64(opts.Delay)
		globalLimiter = rate.NewLimiter(rate.Limit(rps), 1)
		fmt.Printf("Delay %v преобразован в RPS: %.2f (интервал %v)\n", opts.Delay, rps, opts.Delay)
	} else {
		globalLimiter = nil
		fmt.Println("Без ограничения скорости")
	}
}

// waitForRateLimit ожидает разрешения от rate limiter
func waitForRateLimit(ctx context.Context) error {
	if globalLimiter == nil {
		return nil
	}
	start := time.Now()
	err := globalLimiter.Wait(ctx)
	if err == nil {
		elapsed := time.Since(start)
		if elapsed > 0 {
			logMu.Lock()
			now := time.Now()
			if lastLogTime.IsZero() {
				fmt.Printf("[%s] Rate limiter: первый запрос (без задержки)\n", now.Format("15:04:05.000"))
			} else {
				actualInterval := now.Sub(lastLogTime)
				fmt.Printf("[%s] Rate limiter: задержка %.3f сек (фактический интервал %.3f сек)\n",
					now.Format("15:04:05.000"), elapsed.Seconds(), actualInterval.Seconds())
			}
			lastLogTime = now
			logMu.Unlock()
		}
	}
	return err
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	setupRateLimiter(opts)

	workersCount := opts.Workers
	if workersCount <= 0 {
		workersCount = 4
	}

	jobs := make(chan Job, 200)
	results := make(chan Link, 200)

	var wg sync.WaitGroup
	var jobWg sync.WaitGroup

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &jobWg, opts)
		}(i)
	}

	jobWg.Add(1)
	jobs <- Job{
		URL:       opts.URL,
		ParentURL: opts.URL,
		Depth:     int(opts.Depth),
	}

	go func() {
		jobWg.Wait()
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var pagesMap = make(map[string]*Page)
	var brokenLinks []Link

	for result := range results {
		if result.Error != nil {
			brokenLinks = append(brokenLinks, result)
		} else if result.StatusCode != nil && *result.StatusCode >= 400 {
			brokenLinks = append(brokenLinks, result)
		}

		if result.StatusCode != nil && *result.StatusCode < 400 && result.URL != opts.URL {
			pagesMap[result.URL] = &Page{
				URL:          result.URL,
				Depth:        result.Depth,
				HttpStatus:   *result.StatusCode,
				Status:       strings.ToLower(result.ParentStatus),
				Seo:          *result.Seo,
				DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}

	for _, brokenLink := range brokenLinks {
		if brokenLink.Error != nil {
			if page, exists := pagesMap[brokenLink.ParentURL]; exists {
				if page.BrokenLinks == nil {
					page.BrokenLinks = &[]BrokenLink{}
				}
				*page.BrokenLinks = append(*page.BrokenLinks, BrokenLink{
					URL:   brokenLink.URL,
					Error: brokenLink.Error,
				})
			}
		} else if brokenLink.StatusCode != nil {
			if page, exists := pagesMap[brokenLink.ParentURL]; exists {
				if page.BrokenLinks == nil {
					page.BrokenLinks = &[]BrokenLink{}
				}
				*page.BrokenLinks = append(*page.BrokenLinks, BrokenLink{
					URL:        brokenLink.URL,
					StatusCode: brokenLink.StatusCode,
				})
			}
		}
	}

	var pages []Page
	for _, page := range pagesMap {
		pages = append(pages, *page)
	}

	result := Root{
		RootURL:     opts.URL,
		Depth:       int(opts.Depth),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Pages:       pages,
	}

	return json.MarshalIndent(result, "", "  ")
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	jobWg *sync.WaitGroup,
	opts Options,
) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for job := range jobs {
		if job.Depth < 0 {
			jobWg.Done()
			continue
		}

		// Глобальный rate limiter - ЗДЕСЬ, до выполнения запроса
		if err := waitForRateLimit(ctx); err != nil {
			jobWg.Done()
			continue
		}

		fmt.Printf("Worker %d парсит %s, depth:%d\n", id, job.URL, job.Depth)

		html, resp, respStatus, errResp := parseHtml(ctx, job.URL, opts, rng, id)
		if errResp != nil {
			errMsg := errResp.Error()
			select {
			case results <- Link{
				URL:              job.URL,
				Error:            &errMsg,
				ParentURL:        job.ParentURL,
				ParentStatusCode: job.ParentStatusCode,
				ParentStatus:     job.ParentStatus,
			}:
			case <-ctx.Done():
			}
			jobWg.Done()
			continue
		}

		if resp == nil {
			errMsg := "response is nil"
			select {
			case results <- Link{
				URL:              job.URL,
				Error:            &errMsg,
				ParentURL:        job.ParentURL,
				ParentStatusCode: resp.StatusCode,
				ParentStatus:     resp.Status,
			}:
			case <-ctx.Done():
			}
			jobWg.Done()
			continue
		}

		if !IsSameDomain(job.URL, opts.URL) {
			jobWg.Done()
			continue
		}

		seo := getSeoFromHtml(html)

		if job.Depth > 0 {
			links := getLinksFromHtml(html, job.URL, opts)
			for _, link := range links {
				if isNewLink(link) {
					validatedLink, err := NormalizeURL(link, opts.URL)
					if err != nil {
						continue
					}

					visitedMu.Lock()
					if _, exists := visited[link]; !exists {
						visited[link] = struct{}{}
						visitedMu.Unlock()

						jobWg.Add(1)
						select {
						case jobs <- Job{
							URL:              validatedLink,
							ParentURL:        job.URL,
							ParentStatusCode: resp.StatusCode,
							ParentStatus:     respStatus,
							Depth:            job.Depth - 1,
						}:
						case <-ctx.Done():
							jobWg.Done()
							return
						}
					} else {
						visitedMu.Unlock()
					}
				}
			}
		}

		select {
		case results <- Link{
			URL:              job.URL,
			StatusCode:       &resp.StatusCode,
			ParentURL:        job.ParentURL,
			ParentStatusCode: job.ParentStatusCode,
			ParentStatus:     job.ParentStatus,
			Seo:              &seo,
			Depth:            job.Depth,
		}:
		case <-ctx.Done():
			jobWg.Done()
			return
		}

		jobWg.Done()
	}
}

func IsSameDomain(link, domain string) bool {
	domainURL, err := url.Parse(domain)
	if err != nil {
		return false
	}

	linkURL, err := url.Parse(link)
	if err != nil {
		return false
	}

	return strings.ToLower(domainURL.Host) == strings.ToLower(linkURL.Host)
}

func isNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
}

func parseHtml(ctx context.Context, link string, opts Options, rng *rand.Rand, workerID int) (string, *http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant prepare request to url:%s, %w", link, err)
	}

	randomUA := getRandomUserAgent()
	req.Header.Set("User-Agent", randomUA)

	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")

	randomIP := getRandomIP()
	req.Header.Set("X-Forwarded-For", randomIP)

	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")

	if rng.Float64() < 0.7 {
		parts := strings.Split(link, "/")
		if len(parts) > 2 {
			referer := fmt.Sprintf("https://%s/", parts[2])
			req.Header.Set("Referer", referer)
		}
	}

	fmt.Printf("[Worker %d] Время запроса к %s: %s\n", workerID, link, time.Now().Format("15:04:05.000"))

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant handle request to url:%s, %w", link, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant read response body from url:%s, %w", link, err)
	}

	parentStatus := ""
	parts := strings.Split(resp.Status, " ")
	if len(parts) == 2 {
		parentStatus = parts[1]
	}

	return string(body), resp, parentStatus, nil
}

func getLinksFromHtml(htmlBody string, baseURL string, opts Options) []string {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return []string{}
	}

	var links []string

	var findLinks func(*html.Node)
	findLinks = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					normalized, err := NormalizeURL(attr.Val, baseURL)
					if err != nil {
						continue
					}
					links = append(links, normalized)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findLinks(c)
		}
	}

	findLinks(doc)
	return links
}

func getSeoFromHtml(htmlBody string) Seo {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return Seo{
			HasTitle:       false,
			HasDescription: false,
			HasH1:          false,
		}
	}

	seo := Seo{
		HasTitle:       false,
		HasDescription: false,
		HasH1:          false,
	}

	var findElements func(*html.Node)
	findElements = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			titleText := strings.TrimSpace(extractText(n))
			if titleText != "" {
				seo.HasTitle = true
				seo.Title = titleText
			}
		}

		if n.Type == html.ElementNode && n.Data == "meta" {
			var isDescription bool
			var content string
			for _, attr := range n.Attr {
				if attr.Key == "name" && strings.ToLower(attr.Val) == "description" {
					isDescription = true
				}
				if attr.Key == "content" {
					content = attr.Val
				}
			}
			if isDescription && content != "" {
				seo.HasDescription = true
				seo.Description = content
			}
		}

		if n.Type == html.ElementNode && n.Data == "h1" && !seo.HasH1 {
			h1Text := strings.TrimSpace(extractText(n))
			if h1Text != "" {
				seo.HasH1 = true
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findElements(c)
		}
	}

	findElements(doc)
	return seo
}

func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		decoded := html.UnescapeString(n.Data)
		return strings.TrimSpace(decoded)
	}

	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text.WriteString(extractText(c))
	}
	return strings.TrimSpace(text.String())
}

func NormalizeURL(href, baseURL string) (string, error) {
	if strings.HasPrefix(href, "#") {
		return "", fmt.Errorf("skip anchor: %s", href)
	}

	if href == "" {
		return "", fmt.Errorf("empty href")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	parsed, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid href: %w", err)
	}

	resolved := base.ResolveReference(parsed)

	_, err = url.ParseRequestURI(resolved.String())
	if err != nil {
		return "", fmt.Errorf("invalid resolved URL: %w", err)
	}

	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme: %s (only http/https allowed)", resolved.Scheme)
	}

	if resolved.Host == "" {
		return "", fmt.Errorf("no host in URL: %s", resolved.String())
	}

	return resolved.String(), nil
}
