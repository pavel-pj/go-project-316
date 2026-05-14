package crawler

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// WorkerPool пул воркеров
type WorkerPool struct {
	jobs    chan Job
	results chan<- Link
	wg      sync.WaitGroup
	jobWg   sync.WaitGroup
	opts    Options
	parser  *Parser
}

// NewWorkerPool создает пул воркеров
func NewWorkerPool(opts Options, results chan<- Link) *WorkerPool {
	workersCount := opts.Concurrency
	if workersCount <= 0 {
		workersCount = DefaultWorkers
	}

	return &WorkerPool{
		jobs:    make(chan Job, JobQueueSize),
		results: results,
		opts:    opts,
		parser:  NewParser(),
	}
}

// Start запускает воркеров
func (wp *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < wp.opts.Concurrency; i++ {
		wp.wg.Add(1)
		go wp.worker(ctx, i)
	}
}

// AddJob добавляет задачу
func (wp *WorkerPool) AddJob(job Job) {
	wp.jobWg.Add(1)
	wp.jobs <- job
}

// Wait ожидает завершения
func (wp *WorkerPool) Wait() {
	wp.jobWg.Wait()
	close(wp.jobs)
	wp.wg.Wait()
}

func (wp *WorkerPool) worker(ctx context.Context, id int) {
	defer wp.wg.Done()

	httpClient := NewHTTPClient(wp.opts, id)
	normalizedRoot, _ := wp.parser.NormalizeURL(wp.opts.URL, wp.opts.URL)

	for job := range wp.jobs {
		if job.Depth < 0 {
			wp.jobWg.Done()
			continue
		}

		if err := WaitForRateLimit(ctx); err != nil {
			wp.jobWg.Done()
			continue
		}

		resp, err := httpClient.Fetch(ctx, job.URL, false)

		if err != nil || resp == nil || resp.StatusCode >= 400 {
			wp.handleError(job, resp, err)
			wp.jobWg.Done()
			continue
		}

		if !wp.parser.IsSameDomain(job.URL, wp.opts.URL) {
			wp.jobWg.Done()
			continue
		}

		seo, assets := wp.parseContent(resp, job, ctx, httpClient)

		if job.Depth > 0 {
			wp.discoverLinks(resp.Body, job, normalizedRoot, ctx, httpClient)
		}

		wp.sendResult(job, resp, seo, assets)
		wp.jobWg.Done()
	}
}

func (wp *WorkerPool) handleError(job Job, resp *InternalResponse, err error) {
	errMsg := ""

	if err != nil {
		errMsg = err.Error()
	} else if resp != nil && resp.StatusCode >= 400 {
		errMsg = resp.Status
	} else {
		errMsg = "unknown error"
	}

	var statusCode *int
	if resp != nil {
		code := resp.StatusCode
		statusCode = &code
	}

	wp.results <- Link{
		URL:              job.URL,
		StatusCode:       statusCode,
		Error:            errMsg,
		ParentURL:        job.ParentURL,
		ParentStatusCode: job.ParentStatusCode,
		ParentStatus:     job.ParentStatus,
	}
}

func (wp *WorkerPool) parseContent(resp *InternalResponse, job Job, ctx context.Context, httpClient *HTTPClient) (SEO, []Asset) {
	contentType := resp.Header.Get("Content-Type")

	var seo SEO
	var assets []Asset

	switch {
	case strings.Contains(contentType, "text/html"):
		seo = wp.parser.ExtractSEO(resp.Body)
		assets = wp.extractAssets(resp.Body, job.URL, ctx, httpClient)
	case strings.Contains(contentType, "application/rss+xml"),
		strings.Contains(contentType, "application/atom+xml"),
		strings.Contains(contentType, "text/xml"):
		seo = wp.parser.ExtractSEOFromXML(resp.Body)
		assets = []Asset{}
	default:
		seo = wp.parser.emptySEO()
		assets = []Asset{}
	}

	return seo, assets
}

func (wp *WorkerPool) extractAssets(htmlBody, baseURL string, ctx context.Context, httpClient *HTTPClient) []Asset {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return []Asset{}
	}

	var assets []Asset
	assetURLs := make(map[string]bool)

	var findAssets func(*html.Node)
	findAssets = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img", "script":
				wp.processAsset(n, "src", baseURL, assetURLs, &assets, ctx, httpClient)
			case "link":
				wp.processLinkAsset(n, baseURL, assetURLs, &assets, ctx, httpClient)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findAssets(c)
		}
	}

	findAssets(doc)
	return assets
}

func (wp *WorkerPool) processAsset(n *html.Node, attrKey, baseURL string, urlMap map[string]bool, assets *[]Asset, ctx context.Context, httpClient *HTTPClient) {
	for _, attr := range n.Attr {
		if attr.Key == attrKey {
			if assetURL, err := wp.parser.NormalizeURL(attr.Val, baseURL); err == nil {
				if !urlMap[assetURL] {
					urlMap[assetURL] = true
					asset := wp.fetchAsset(ctx, assetURL, httpClient)
					*assets = append(*assets, asset)
				}
			}
		}
	}
}

func (wp *WorkerPool) processLinkAsset(n *html.Node, baseURL string, urlMap map[string]bool, assets *[]Asset, ctx context.Context, httpClient *HTTPClient) {
	var isStylesheet bool
	var href string

	for _, attr := range n.Attr {
		if attr.Key == "rel" && strings.Contains(strings.ToLower(attr.Val), "stylesheet") {
			isStylesheet = true
		}
		if attr.Key == "href" {
			href = attr.Val
		}
	}

	if isStylesheet && href != "" {
		if assetURL, err := wp.parser.NormalizeURL(href, baseURL); err == nil {
			if !urlMap[assetURL] {
				urlMap[assetURL] = true
				asset := wp.fetchAsset(ctx, assetURL, httpClient)
				*assets = append(*assets, asset)
			}
		}
	}
}

func (wp *WorkerPool) fetchAsset(ctx context.Context, assetURL string, httpClient *HTTPClient) Asset {
	// Check cache
	assetsCacheMu.RLock()
	if cached, exists := assetsCache[assetURL]; exists {
		assetsCacheMu.RUnlock()
		return cached
	}
	assetsCacheMu.RUnlock()

	if err := WaitForRateLimit(ctx); err != nil {
		return Asset{
			URL:        assetURL,
			Type:       GetAssetType(assetURL, ""),
			StatusCode: 0,
			SizeBytes:  0,
			Error:      err.Error(),
		}
	}

	resp, err := httpClient.Fetch(ctx, assetURL, true)

	asset := Asset{
		URL:        assetURL,
		Type:       GetAssetType(assetURL, ""),
		StatusCode: 0,
		SizeBytes:  0,
	}

	if err != nil {
		asset.Error = err.Error()
		return asset
	}

	if resp != nil {
		asset.StatusCode = resp.StatusCode
		if resp.StatusCode >= 400 {
			asset.Error = resp.Status
		} else {
			asset.Type = GetAssetType(assetURL, resp.Header.Get("Content-Type"))
			asset.SizeBytes = int64(len(resp.Body))
		}
	}

	assetsCacheMu.Lock()
	assetsCache[assetURL] = asset
	assetsCacheMu.Unlock()

	return asset
}

func (wp *WorkerPool) discoverLinks(htmlBody string, job Job, normalizedRoot string, ctx context.Context, httpClient *HTTPClient) {
	links := wp.parser.ExtractLinks(htmlBody, job.URL)
	for _, link := range links {
		validatedLink, err := wp.parser.NormalizeURL(link, wp.opts.URL)
		if err != nil {
			continue
		}

		if validatedLink == normalizedRoot {
			continue
		}

		if IsNewLink(validatedLink) {
			MarkVisited(validatedLink)

			wp.jobWg.Add(1)
			select {
			case wp.jobs <- Job{
				URL:              validatedLink,
				ParentURL:        job.URL,
				ParentStatusCode: 200,
				ParentStatus:     "OK",
				Depth:            job.Depth - 1,
			}:
			case <-ctx.Done():
				wp.jobWg.Done()
				return
			}
		}
	}
}

func (wp *WorkerPool) sendResult(job Job, resp *InternalResponse, seo SEO, assets []Asset) {
	statusCode := resp.StatusCode
	wp.results <- Link{
		URL:              job.URL,
		StatusCode:       &statusCode,
		ParentURL:        job.ParentURL,
		ParentStatusCode: job.ParentStatusCode,
		ParentStatus:     job.ParentStatus,
		SEO:              &seo,
		Depth:            job.Depth,
		Assets:           assets,
	}
}
