package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

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

	// Normalize root URL
	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	// Clear visited map at start
	visitedMu.Lock()
	visited = make(map[string]struct{})
	visitedMu.Unlock()

	select {
	case jobs <- Job{
		URL:       opts.URL, // Use original URL, not normalized
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
	rootAdded := false

	for result := range results {
		// Normalize URL for comparison but keep original for URL field
		normalizedURL, _ := NormalizeURL(result.URL, opts.URL)

		// For root URL detection, compare normalized versions
		isRoot := normalizedURL == normalizedRoot ||
			strings.TrimSuffix(normalizedURL, "/") == strings.TrimSuffix(normalizedRoot, "/")

		// Collect broken links (including 404s)
		if result.Error != nil || (result.StatusCode != nil && *result.StatusCode >= 400) {
			brokenLinks = append(brokenLinks, result)

			// For missing.test: add the root page even if it returns 404
			if isRoot && !rootAdded {
				rootAdded = true
				seo := SEO{
					HasTitle:       false,
					Title:          "",
					HasDescription: false,
					Description:    "",
					HasH1:          false,
				}

				statusCode := 404
				if result.StatusCode != nil {
					statusCode = *result.StatusCode
				}

				pagesMap[normalizedRoot] = &Page{
					URL:          opts.URL,
					Depth:        0,
					HttpStatus:   statusCode,
					Status:       "error",
					SEO:          seo,
					Assets:       []Asset{},
					BrokenLinks:  []BrokenLink{},
					DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
				}
			}
			continue
		}

		// Process successful pages (status code < 400)
		if result.StatusCode != nil && *result.StatusCode < 400 {
			status := strings.ToLower(result.ParentStatus)
			if status == "" {
				status = "ok"
			}

			seo := SEO{
				HasTitle:       false,
				Title:          "",
				HasDescription: false,
				Description:    "",
				HasH1:          false,
			}

			if result.SEO != nil {
				seo.HasTitle = result.SEO.HasTitle
				seo.Title = result.SEO.Title
				seo.HasDescription = result.SEO.HasDescription
				seo.Description = result.SEO.Description
				seo.HasH1 = result.SEO.HasH1
			}

			// Calculate depth - root page is always depth 0
			var pageDepth int
			if isRoot {
				pageDepth = 0
			} else {
				pageDepth = int(opts.Depth) - result.Depth
				if pageDepth < 1 {
					pageDepth = 1
				}
			}

			// Use normalized URL as map key to avoid duplicates
			mapKey := normalizedURL
			if mapKey == "" {
				mapKey = result.URL
			}

			// If this is root and we already have it, skip adding duplicate
			if isRoot && rootAdded {
				continue
			}

			if isRoot {
				rootAdded = true
			}

			assets := result.Assets
			if assets == nil {
				assets = []Asset{}
			}

			pagesMap[mapKey] = &Page{
				URL:          result.URL, // Use original URL (without trailing slash for root)
				Depth:        pageDepth,
				HttpStatus:   *result.StatusCode,
				Status:       status,
				SEO:          seo,
				Assets:       assets,
				BrokenLinks:  []BrokenLink{},
				DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}

	// Add broken links to pages
	for _, brokenLink := range brokenLinks {
		// Normalize parent URL
		normalizedParent, _ := NormalizeURL(brokenLink.ParentURL, opts.URL)

		// Also try without trailing slash
		parentKey := strings.TrimSuffix(normalizedParent, "/")

		var page *Page
		var exists bool

		if page, exists = pagesMap[normalizedParent]; !exists {
			page, exists = pagesMap[parentKey]
		}

		if exists && page != nil {
			// Check if link already exists
			alreadyExists := false
			for _, existing := range page.BrokenLinks {
				if existing.URL == brokenLink.URL {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				bl := BrokenLink{
					URL: brokenLink.URL,
				}
				if brokenLink.StatusCode != nil {
					bl.StatusCode = *brokenLink.StatusCode
				}
				if brokenLink.Error != nil {
					bl.Error = *brokenLink.Error
				}
				page.BrokenLinks = append(page.BrokenLinks, bl)
			}
		}
	}

	var pages []Page
	for _, page := range pagesMap {
		// Ensure Assets is never nil
		if page.Assets == nil {
			page.Assets = []Asset{}
		}

		// Ensure BrokenLinks is never nil
		if page.BrokenLinks == nil {
			page.BrokenLinks = []BrokenLink{}
		}

		// Sort assets by URL for consistent ordering
		if len(page.Assets) > 1 {
			sort.Slice(page.Assets, func(i, j int) bool {
				if page.Assets[i].Type != page.Assets[j].Type {
					return page.Assets[i].Type < page.Assets[j].Type
				}
				return page.Assets[i].URL < page.Assets[j].URL
			})
		}

		// Sort broken links by URL
		if len(page.BrokenLinks) > 1 {
			sort.Slice(page.BrokenLinks, func(i, j int) bool {
				return page.BrokenLinks[i].URL < page.BrokenLinks[j].URL
			})
		}

		pages = append(pages, *page)
	}

	// Sort pages by URL
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].URL < pages[j].URL
	})

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

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	jobWg *sync.WaitGroup,
	opts Options,
) {

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

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
		// Important: close resp.Body after use
		defer func() {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}()

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
				_ = err
			}
		}

		if !IsSameDomain(job.URL, opts.URL) {
			jobWg.Done()
			continue
		}

		// Check content type to determine if we should parse as HTML
		contentType := resp.Header.Get("Content-Type")
		isXML := strings.Contains(contentType, "application/xml") ||
			strings.Contains(contentType, "text/xml") ||
			strings.Contains(contentType, "application/rss+xml") ||
			strings.Contains(contentType, "application/atom+xml")

		var seo SEO
		var assets []Asset

		if isXML {
			// For XML/RSS feeds, don't parse as HTML
			seo = SEO{
				HasTitle:       false,
				Title:          "",
				HasDescription: false,
				Description:    "",
				HasH1:          false,
			}
			assets = []Asset{}
		} else {
			seo = getSeoFromHtml(html)
			assets = extractAssetsFromHtml(html, job.URL, opts, ctx, rng, id)
		}

		if job.Depth > 0 {
			links := getLinksFromHtml(html, job.URL, opts)
			for _, link := range links {
				validatedLink, err := NormalizeURL(link, opts.URL)
				if err != nil {
					continue
				}

				// Skip adding root URL as a child page
				if validatedLink == normalizedRoot {
					continue
				}

				if isNewLink(validatedLink) {
					visitedMu.Lock()
					if _, exists := visited[validatedLink]; !exists {
						visited[validatedLink] = struct{}{}
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
