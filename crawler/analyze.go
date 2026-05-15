package crawler

import (
	"context"
	"io"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	var pendingJobs int64

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &pendingJobs, opts)
		}(i)
	}

	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	visitedMu.Lock()
	visited = make(map[string]struct{})
	visitedMu.Unlock()

	atomic.AddInt64(&pendingJobs, 1)
	jobs <- Job{
		URL:       opts.URL,
		ParentURL: opts.URL,
		Depth:     int(opts.Depth),
	}

	go func() {
		for {
			if atomic.LoadInt64(&pendingJobs) == 0 {
				close(jobs)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	reporter := NewReporter(opts, normalizedRoot)
	reporter.ProcessResults(results)

	return reporter.BuildFinalJSON()
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	pendingJobs *int64,
	opts Options,
) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	for job := range jobs {
		func(currentJob Job) {
			if currentJob.Depth < 0 {
				return
			}

			if err := waitForRateLimit(ctx); err != nil {
				return
			}

			html, resp, respStatus, errResp := fetchWithRetry(ctx, currentJob.URL, opts, rng, id, false)

			if errResp != nil {
				errMsg := errResp.Error()
				var statusCode *int
				if resp != nil {
					code := resp.StatusCode
					statusCode = &code
				}
				select {
				case results <- Link{
					URL:              currentJob.URL,
					StatusCode:       statusCode,
					Error:            errMsg,
					ParentURL:        currentJob.ParentURL,
					ParentStatusCode: currentJob.ParentStatusCode,
					ParentStatus:     currentJob.ParentStatus,
				}:
				case <-ctx.Done():
				}
				return
			}

			if resp == nil {
				select {
				case results <- Link{
					URL:              currentJob.URL,
					Error:            "response is nil",
					ParentURL:        currentJob.ParentURL,
					ParentStatusCode: 0,
					ParentStatus:     "",
				}:
				case <-ctx.Done():
				}
				return
			}

			if resp.StatusCode >= 400 {
				code := resp.StatusCode
				select {
				case results <- Link{
					URL:              currentJob.URL,
					StatusCode:       &code,
					Error:            resp.Status,
					ParentURL:        currentJob.ParentURL,
					ParentStatusCode: currentJob.ParentStatusCode,
					ParentStatus:     currentJob.ParentStatus,
				}:
				case <-ctx.Done():
				}
				return
			}

			if resp.Body != nil {
				body, err := io.ReadAll(resp.Body)
				if err == nil {
					html = string(body)
				}

				// можно залогировать, но для тестов просто игнорируем
				_ = resp.Body.Close()

			}

			if !IsSameDomain(currentJob.URL, opts.URL) {
				return
			}

			contentType := resp.Header.Get("Content-Type")

			var seo SEO
			var assets []Asset

			switch {
			case strings.Contains(contentType, "text/html"):
				seo = getSeoFromHtml(html)
				assets = extractAssetsFromHtml(html, currentJob.URL, opts, ctx, rng, id)
			case strings.Contains(contentType, "application/rss+xml"),
				strings.Contains(contentType, "application/atom+xml"),
				strings.Contains(contentType, "text/xml"):
				seo = getSeoFromXml(html)
				assets = []Asset{}
			default:
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

			if currentJob.Depth > 0 {
				links := getLinksFromHtml(html, currentJob.URL, opts)
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

							atomic.AddInt64(pendingJobs, 1)

							jobs <- Job{
								URL:              validatedLink,
								ParentURL:        currentJob.URL,
								ParentStatusCode: resp.StatusCode,
								ParentStatus:     respStatus,
								Depth:            currentJob.Depth - 1,
							}
						} else {
							visitedMu.Unlock()
						}
					}
				}
			}

			results <- Link{
				URL:              currentJob.URL,
				StatusCode:       &resp.StatusCode,
				ParentURL:        currentJob.ParentURL,
				ParentStatusCode: currentJob.ParentStatusCode,
				ParentStatus:     currentJob.ParentStatus,
				SEO:              &seo,
				Depth:            currentJob.Depth,
				Assets:           assets,
			}
		}(job)

		atomic.AddInt64(pendingJobs, -1)
	}
}
