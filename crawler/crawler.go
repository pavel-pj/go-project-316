package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

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

// getRandomUserAgent возвращает случайный User-Agent
func getRandomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return userAgents[rng.Intn(len(userAgents))]
}

// getRandomIP возвращает случайный IP адрес
//
//nolint:gosec
func getRandomIP() string {

	return fmt.Sprintf("%d.%d.%d.%d",
		//nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255)) //nolint:gosec
}

// setupRateLimiter настраивает глобальный лимитер запросов
func setupRateLimiter(opts Options) {
	switch {
	case opts.RPS > 0:
		globalLimiter = rate.NewLimiter(rate.Limit(opts.RPS), 1)
	case opts.Delay > 0:
		rps := float64(time.Second) / float64(opts.Delay)
		globalLimiter = rate.NewLimiter(rate.Limit(rps), 1)
	default:
		globalLimiter = nil
	}
}

// waitForRateLimit ожидает разрешения от rate limiter
func waitForRateLimit(ctx context.Context) error {
	if globalLimiter == nil {
		return nil
	}
	if err := globalLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait failed: %w", err)
	}
	return nil
}

// Analyze запускает процесс обхода сайта
func Analyze(ctx context.Context, opts Options) ([]byte, error) {

	setupRateLimiter(opts)

	workersCount := opts.Concurrency
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

	select {
	case jobs <- Job{
		URL:       opts.URL,
		ParentURL: opts.URL,
		Depth:     int(opts.Depth),
	}:
		jobWg.Add(1)
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
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

		if result.StatusCode != nil && *result.StatusCode < 400 {
			pagesMap[result.URL] = &Page{
				URL:          result.URL,
				Depth:        result.Depth,
				HttpStatus:   *result.StatusCode,
				Status:       strings.ToLower(result.ParentStatus),
				SEO:          *result.SEO,
				Assets:       result.Assets,
				DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}

	for _, brokenLink := range brokenLinks {
		if page, exists := pagesMap[brokenLink.ParentURL]; exists {
			if page.BrokenLinks == nil {
				page.BrokenLinks = &[]BrokenLink{}
			}

			bl := BrokenLink{
				URL: brokenLink.URL,
			}

			if brokenLink.StatusCode != nil {
				bl.StatusCode = brokenLink.StatusCode
			}

			if brokenLink.Error != nil {
				bl.Error = brokenLink.Error
			}

			*page.BrokenLinks = append(*page.BrokenLinks, bl)
		}
	}

	var pages []Page
	for _, page := range pagesMap {
		pages = append(pages, *page)
	}

	result := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Pages:       pages,
	}

	var jsonResult []byte
	var err error

	if opts.IndentJSON {
		jsonResult, err = json.MarshalIndent(result, "", "  ")
	} else {
		jsonResult, err = json.Marshal(result)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return jsonResult, nil
}

func getAssetType(urlStr string, contentType string) string {
	if contentType != "" {
		contentType = strings.ToLower(contentType)
		switch {
		case strings.Contains(contentType, "image"):
			return "image"
		case strings.Contains(contentType, "javascript"), strings.Contains(contentType, "ecmascript"):
			return "script"
		case strings.Contains(contentType, "css"):
			return "style"
		}
	}

	urlLower := strings.ToLower(urlStr)
	switch {
	case strings.Contains(urlLower, ".jpg"), strings.Contains(urlLower, ".jpeg"),
		strings.Contains(urlLower, ".png"), strings.Contains(urlLower, ".gif"),
		strings.Contains(urlLower, ".svg"), strings.Contains(urlLower, ".webp"),
		strings.Contains(urlLower, ".ico"):
		return "image"
	case strings.Contains(urlLower, ".js"):
		return "script"
	case strings.Contains(urlLower, ".css"):
		return "style"
	default:
		return "other"
	}
}

func fetchAsset(ctx context.Context, assetURL string, opts Options, rng *rand.Rand, workerID int) Asset {
	// Проверяем кэш
	assetsCacheMu.RLock()
	if cached, exists := assetsCache[assetURL]; exists {
		assetsCacheMu.RUnlock()
		fmt.Printf("[Worker %d] Ассет из кэша: %s\n", workerID, assetURL)
		return cached
	}
	assetsCacheMu.RUnlock()

	// Ждём rate limiter
	if err := waitForRateLimit(ctx); err != nil {
		return Asset{
			URL:        assetURL,
			Type:       getAssetType(assetURL, ""),
			StatusCode: 0,
			SizeBytes:  0,
			Error:      err.Error(),
		}
	}

	// Выполняем запрос с retry
	_, resp, _, err := fetchWithRetry(ctx, assetURL, opts, rng, workerID, true)

	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				fmt.Printf("[Worker %d] Ошибка закрытия тела ответа: %v\n", workerID, err)
			}
		}()
	}

	asset := Asset{
		URL:        assetURL,
		Type:       getAssetType(assetURL, ""),
		StatusCode: 0,
		SizeBytes:  0,
	}

	if err != nil {
		asset.Error = err.Error()
	} else if resp != nil {
		asset.StatusCode = resp.StatusCode

		if resp.StatusCode >= 400 {
			asset.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)
		} else {
			contentType := resp.Header.Get("Content-Type")
			asset.Type = getAssetType(assetURL, contentType)

			contentLength := resp.Header.Get("Content-Length")
			if contentLength != "" {
				if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
					asset.SizeBytes = size
				} else {
					body, err := io.ReadAll(resp.Body)
					if err == nil {
						asset.SizeBytes = int64(len(body))
					} else {
						asset.Error = fmt.Sprintf("failed to read body: %v", err)
					}
					resp.Body = io.NopCloser(strings.NewReader(string(body)))
				}
			} else {
				body, err := io.ReadAll(resp.Body)
				if err == nil {
					asset.SizeBytes = int64(len(body))
				} else {
					asset.Error = fmt.Sprintf("failed to read body: %v", err)
				}
				resp.Body = io.NopCloser(strings.NewReader(string(body)))
			}
		}
	}

	assetsCacheMu.Lock()
	assetsCache[assetURL] = asset
	assetsCacheMu.Unlock()

	return asset
}

func extractAssetsFromHtml(htmlBody string, baseURL string, opts Options, ctx context.Context, rng *rand.Rand, workerID int) []Asset {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML для ассетов: %v\n", err)
		return []Asset{}
	}

	var assets []Asset
	assetURLs := make(map[string]bool)

	var findAssets func(*html.Node)
	findAssets = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img":
				for _, attr := range n.Attr {
					if attr.Key == "src" {
						if assetURL, err := NormalizeURL(attr.Val, baseURL); err == nil {
							if !assetURLs[assetURL] {
								assetURLs[assetURL] = true
								asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
								assets = append(assets, asset)
							}
						}
					}
				}
			case "script":
				for _, attr := range n.Attr {
					if attr.Key == "src" {
						if assetURL, err := NormalizeURL(attr.Val, baseURL); err == nil {
							if !assetURLs[assetURL] {
								assetURLs[assetURL] = true
								asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
								assets = append(assets, asset)
							}
						}
					}
				}
			case "link":
				for _, attr := range n.Attr {
					if attr.Key == "rel" && strings.Contains(strings.ToLower(attr.Val), "stylesheet") {
						for _, attr2 := range n.Attr {
							if attr2.Key == "href" {
								if assetURL, err := NormalizeURL(attr2.Val, baseURL); err == nil {
									if !assetURLs[assetURL] {
										assetURLs[assetURL] = true
										asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
										assets = append(assets, asset)
									}
								}
							}
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findAssets(c)
		}
	}

	findAssets(doc)
	return assets
}

// isRetriableError определяет, можно ли повторить запрос
func isRetriableError(err error, resp *http.Response) bool {
	if err != nil {
		errStr := err.Error()
		temporaryErrors := []string{
			"connection refused",
			"connection reset",
			"EOF",
			"timeout",
			"temporary failure",
			"no such host",
			"network is unreachable",
			"connection timed out",
			"i/o timeout",
			"broken pipe",
			"context deadline exceeded",
		}

		for _, tempErr := range temporaryErrors {
			if strings.Contains(strings.ToLower(errStr), tempErr) {
				return true
			}
		}
		return false
	}

	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusRequestTimeout,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return false
		}
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return true
		}
	}

	return false
}

func getRetryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	// nolint:gosec  // attempt всегда >=0 в нашем коде
	shift := uint(attempt)
	if shift > 30 {
		shift = 30
	}
	delay := time.Duration(1<<shift) * time.Second
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}

	jitter := time.Duration(rand.Int63n(int64(delay / 5)))
	if rand.Intn(2) == 0 {
		delay += jitter
	} else {
		delay -= jitter
	}

	if delay < 0 {
		delay = 0
	}

	return delay
}

func doRequest(ctx context.Context, link string, opts Options, rng *rand.Rand, workerID int) (string, *http.Response, string, error) {
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

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant handle request to url:%s, %w", link, err)
	}

	parentStatus := ""
	parts := strings.Split(resp.Status, " ")
	if len(parts) == 2 {
		parentStatus = parts[1]
	}

	return "", resp, parentStatus, nil
}

func fetchWithRetry(ctx context.Context, link string, opts Options, rng *rand.Rand, workerID int, isAsset bool) (string, *http.Response, string, error) {
	var lastResp *http.Response
	var lastErr error
	var lastBody string
	var lastStatus string

	maxRetries := opts.Retries
	if maxRetries < 0 {
		maxRetries = 0
	}

	if isAsset && maxRetries > 2 {
		maxRetries = 2
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", lastResp, "", fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		if attempt > 0 {
			delay := getRetryDelay(attempt-1, lastResp)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", lastResp, "", fmt.Errorf("context cancelled: %w", ctx.Err())
			}
		}

		body, resp, status, err := doRequest(ctx, link, opts, rng, workerID)
		lastResp = resp
		lastErr = err
		lastBody = body
		lastStatus = status

		if err == nil && resp != nil && resp.StatusCode < 400 {
			return body, resp, status, nil
		}

		if attempt == maxRetries {
			return body, resp, status, err
		}

		if !isRetriableError(err, resp) {
			return body, resp, status, err
		}
	}

	return lastBody, lastResp, lastStatus, lastErr
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

		if err := waitForRateLimit(ctx); err != nil {
			jobWg.Done()
			continue
		}

		html, resp, respStatus, errResp := fetchWithRetry(ctx, job.URL, opts, rng, id, false)

		if errResp != nil {
			errMsg := errResp.Error()
			var statusCode *int
			if resp != nil {
				code := resp.StatusCode
				statusCode = &code
			}

			select {
			case results <- Link{
				URL:              job.URL,
				StatusCode:       statusCode,
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
				ParentStatusCode: 0,
				ParentStatus:     "",
			}:
			case <-ctx.Done():
			}
			jobWg.Done()
			continue
		}

		if resp.StatusCode >= 400 {
			errMsg := resp.Status
			code := resp.StatusCode

			select {
			case results <- Link{
				URL:              job.URL,
				StatusCode:       &code,
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

		if resp.Body != nil {
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				html = string(body)
			}
			if err := resp.Body.Close(); err != nil {
				fmt.Printf("Worker %d: ошибка закрытия тела ответа: %v\n", id, err)
			}
		}

		if !IsSameDomain(job.URL, opts.URL) {
			jobWg.Done()
			continue
		}

		seo := getSeoFromHtml(html)
		assets := extractAssetsFromHtml(html, job.URL, opts, ctx, rng, id)

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

		resultLink := Link{
			URL:              job.URL,
			StatusCode:       &resp.StatusCode,
			ParentURL:        job.ParentURL,
			ParentStatusCode: job.ParentStatusCode,
			ParentStatus:     job.ParentStatus,
			SEO:              &seo,
			Depth:            job.Depth,
			Assets:           assets,
		}

		select {
		case results <- resultLink:
		case <-ctx.Done():
			jobWg.Done()
			return
		}

		jobWg.Done()
	}
}

// IsSameDomain проверяет, принадлежат ли ссылки одному домену
func IsSameDomain(link, domain string) bool {
	domainURL, err := url.Parse(domain)
	if err != nil {
		return false
	}

	linkURL, err := url.Parse(link)
	if err != nil {
		return false
	}

	return strings.EqualFold(domainURL.Host, linkURL.Host)
}

func isNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
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

func getSeoFromHtml(htmlBody string) SEO {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return SEO{
			HasTitle:       false,
			HasDescription: false,
			HasH1:          false,
		}
	}

	seo := SEO{
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

// NormalizeURL нормализует URL
func NormalizeURL(href, baseURL string) (string, error) {
	if strings.HasPrefix(href, "#") {
		return "", errors.New("skip anchor")
	}

	if href == "" {
		return "", errors.New("empty href")
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
