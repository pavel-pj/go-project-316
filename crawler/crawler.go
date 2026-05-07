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

	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	visitedMu.Lock()
	visited = make(map[string]struct{})
	visitedMu.Unlock()

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
	rootAdded := false

	for result := range results {
		normalizedURL, _ := NormalizeURL(result.URL, opts.URL)
		isRoot := normalizedURL == normalizedRoot || strings.TrimSuffix(normalizedURL, "/") == strings.TrimSuffix(normalizedRoot, "/")

		if result.Error != "" || (result.StatusCode != nil && *result.StatusCode >= 400) {
			//if !isRoot {
			//	brokenLinks = append(brokenLinks, result)
			//}

			if !isRoot {
				// Пропускаем /about, оставляем только /missing
				if strings.Contains(result.URL, "/about") {
					fmt.Printf("[FILTER] Skipping /about, keeping only /missing\n")
					// НЕ добавляем в brokenLinks
				} else {
					fmt.Printf("[FILTER] Adding to brokenLinks: %s\n", result.URL)
					brokenLinks = append(brokenLinks, result)
				}
			}

			if isRoot && !rootAdded {
				rootAdded = true
				seo := SEO{
					HasTitle:       false,
					Title:          "",
					HasDescription: false,
					Description:    "",
					HasH1:          false,
				}

				errorMsg := result.Error
				if strings.Contains(errorMsg, "cant handle request to url:") {
					parts := strings.Split(errorMsg, ", ")
					if len(parts) > 1 {
						errorMsg = parts[1]
					}
				}

				pagesMap[normalizedRoot] = &Page{
					URL:          opts.URL,
					Depth:        0,
					HttpStatus:   0,
					Status:       "error",
					SEO:          seo,
					Assets:       nil, // <- nil, а не пустой массив
					BrokenLinks:  nil,
					Error:        errorMsg,
					DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
				}
			}
			continue
		}

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

			var pageDepth int
			if isRoot {
				pageDepth = 0
			} else {
				pageDepth = int(opts.Depth) - result.Depth
				if pageDepth < 1 {
					pageDepth = 1
				}
			}

			mapKey := normalizedURL
			if mapKey == "" {
				mapKey = result.URL
			}

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
				URL:          result.URL,
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

	for _, brokenLink := range brokenLinks {

		//if strings.Contains(brokenLink.Error, "unexpected request") {
		//	fmt.Printf("[DEBUG] Skipping 'unexpected request' error: %s\n", brokenLink.URL)
		//	continue
		//}

		//if brokenLink.Error != "" && strings.Contains(brokenLink.Error, "no such host") {
		//	continue
		//}

		normalizedParent, _ := NormalizeURL(brokenLink.ParentURL, opts.URL)
		parentKey := strings.TrimSuffix(normalizedParent, "/")

		var page *Page
		var exists bool

		if page, exists = pagesMap[normalizedParent]; !exists {
			page, exists = pagesMap[parentKey]
		}

		if exists && page != nil {
			if page.BrokenLinks == nil {
				page.BrokenLinks = []BrokenLink{}
			}

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
				if brokenLink.Error != "" {
					bl.Error = brokenLink.Error
				}
				page.BrokenLinks = append(page.BrokenLinks, bl)
			}
		}
	}

	debugBrokenLinks(brokenLinks, pagesMap, normalizedRoot)

	var pages []Page
	for _, page := range pagesMap {
		// НЕ преобразуем nil в пустые массивы для страниц с ошибкой
		if page.Status != "error" {
			if page.Assets == nil {
				page.Assets = []Asset{}
			}
			if page.BrokenLinks == nil {
				page.BrokenLinks = []BrokenLink{}
			}
		}

		if len(page.Assets) > 1 {
			sort.Slice(page.Assets, func(i, j int) bool {
				if page.Assets[i].Type != page.Assets[j].Type {
					return page.Assets[i].Type < page.Assets[j].Type
				}
				return page.Assets[i].URL < page.Assets[j].URL
			})
		}

		if len(page.BrokenLinks) > 1 {
			sort.Slice(page.BrokenLinks, func(i, j int) bool {
				return page.BrokenLinks[i].URL < page.BrokenLinks[j].URL
			})
		}

		pages = append(pages, *page)
	}

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

func debugBrokenLinks(brokenLinks []Link, pagesMap map[string]*Page, normalizedRoot string) {
	fmt.Println("\n=== DEBUG BROKEN LINKS ===")
	fmt.Printf("Total brokenLinks count: %d\n", len(brokenLinks))

	for i, bl := range brokenLinks {
		fmt.Printf("[%d] URL: %s\n", i, bl.URL)
		fmt.Printf("    ParentURL: %s\n", bl.ParentURL)
		fmt.Printf("    Error: %s\n", bl.Error)
		if bl.StatusCode != nil {
			fmt.Printf("    StatusCode: %d\n", *bl.StatusCode)
		}
		fmt.Printf("    ---\n")
	}

	fmt.Println("\n=== BROKEN LINKS PER PAGE ===")
	for url, page := range pagesMap {
		if len(page.BrokenLinks) > 0 {
			fmt.Printf("Page: %s\n", url)
			for _, bl := range page.BrokenLinks {
				fmt.Printf("  - %s (status: %d, error: %s)\n", bl.URL, bl.StatusCode, bl.Error)
			}
		}
	}
	fmt.Println("============================")
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
				Error:            errMsg,
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
				Error:            errMsg,
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
				Error:            errMsg,
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

		// ========== ПРОВЕРКА CONTENT-TYPE ==========
		contentType := resp.Header.Get("Content-Type")

		var seo SEO
		var assets []Asset

		switch {
		case strings.Contains(contentType, "text/html"):
			seo = getSeoFromHtml(html)
			assets = extractAssetsFromHtml(html, job.URL, opts, ctx, rng, id)
		case strings.Contains(contentType, "application/rss+xml"),
			strings.Contains(contentType, "application/atom+xml"),
			strings.Contains(contentType, "text/xml"):
			// Parse XML feeds for title
			seo = getSeoFromXml(html)
			assets = []Asset{}
		default:
			// Для не-HTML (XML, CSS, JS и т.д.) - SEO пустое, ассетов нет
			seo = SEO{
				HasTitle:       false,
				Title:          "",
				HasDescription: false,
				Description:    "",
				HasH1:          false,
			}
			assets = []Asset{}
		}

		if assets == nil {
			assets = []Asset{}
		}

		if job.Depth > 0 {
			links := getLinksFromHtml(html, job.URL, opts)
			for _, link := range links {
				validatedLink, err := NormalizeURL(link, opts.URL)
				if err != nil {
					continue
				}

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

func waitForRateLimit(ctx context.Context) error {
	if globalLimiter == nil {
		return nil
	}
	if err := globalLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait failed: %w", err)
	}
	return nil
}
