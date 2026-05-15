package crawler

import (
	"context"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"
)

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	wg *sync.WaitGroup,
	opts Options,
) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	for job := range jobs {
		if job.Depth < 0 {
			continue
		}

		if err := waitForRateLimit(ctx); err != nil {
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
				Error:            errMsg,
				ParentURL:        job.ParentURL,
				ParentStatusCode: job.ParentStatusCode,
				ParentStatus:     job.ParentStatus,
			}:
			case <-ctx.Done():
			}
			continue
		}

		if resp == nil {
			select {
			case results <- Link{
				URL:              job.URL,
				Error:            "response is nil",
				ParentURL:        job.ParentURL,
				ParentStatusCode: 0,
				ParentStatus:     "",
			}:
			case <-ctx.Done():
			}
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
			continue
		}

		if resp.Body != nil {
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				html = string(body)
			}
			resp.Body.Close()
		}

		if !IsSameDomain(job.URL, opts.URL) {
			continue
		}

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

						wg.Add(1)
						go func(linkURL, parentURL string, parentCode int, parentStatus string, depth int) {
							defer wg.Done()
							select {
							case jobs <- Job{
								URL:              linkURL,
								ParentURL:        parentURL,
								ParentStatusCode: parentCode,
								ParentStatus:     parentStatus,
								Depth:            depth,
							}:
							case <-ctx.Done():
							}
						}(validatedLink, job.URL, resp.StatusCode, respStatus, job.Depth-1)
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
			return
		}

		wg.Done()
	}
}
