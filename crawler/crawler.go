package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
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
		// Собираем битые ссылки
		if result.Error != nil {
			brokenLinks = append(brokenLinks, result)
		} else if result.StatusCode != nil && *result.StatusCode >= 400 {
			brokenLinks = append(brokenLinks, result)
		}

		// Обрабатываем успешные страницы
		if result.StatusCode != nil && *result.StatusCode < 400 {
			status := strings.ToLower(result.ParentStatus)
			if status == "" {
				status = "ok"
			}
			pagesMap[result.URL] = &Page{
				URL:          result.URL,
				Depth:        0,
				HttpStatus:   *result.StatusCode,
				Status:       status,
				SEO:          *result.SEO,
				Assets:       result.Assets,
				BrokenLinks:  []BrokenLink{},
				DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
	}

	// Добавляем битые ссылки к страницам
	for _, brokenLink := range brokenLinks {
		if page, exists := pagesMap[brokenLink.ParentURL]; exists {
			// Проверяем, нет ли уже такой ссылки
			alreadyExists := false
			for _, existing := range page.BrokenLinks {
				if existing.URL == brokenLink.URL {
					alreadyExists = true
					break
				}
			}
			if alreadyExists {
				continue
			}

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
		// Важно: закрыть resp.Body после использования
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
