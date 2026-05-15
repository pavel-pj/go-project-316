package crawler

import (
	"context"
	"sync"
)

/*
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

	reporter := NewReporter(opts, normalizedRoot)
	reporter.ProcessResults(results)

	return reporter.BuildFinalJSON()

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

		// ПРОВЕРКА CONTENT-TYPE
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
*/
/*
var totalJobs int64

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	fmt.Println("[DEBUG] Analyze: START")
	atomic.StoreInt64(&totalJobs, 0)

	setupRateLimiter(opts)

	workersCount := opts.Concurrency
	if workersCount <= 0 {
		workersCount = 4
	}
	fmt.Printf("[DEBUG] Analyze: workersCount = %d\n", workersCount)

	jobs := make(chan Job, 200)
	results := make(chan Link, 200)

	var wg sync.WaitGroup

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		fmt.Printf("[DEBUG] Analyze: starting worker %d\n", i)
		go func(id int) {
			defer func() {
				fmt.Printf("[DEBUG] Worker %d: defer wg.Done() called\n", id)
				wg.Done()
			}()
			worker(id, ctx, jobs, results, &wg, opts)
		}(i)
	}

	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	visitedMu.Lock()
	visited = make(map[string]struct{})
	visitedMu.Unlock()

	fmt.Println("[DEBUG] Analyze: sending initial job")
	wg.Add(1)
	atomic.AddInt64(&totalJobs, 1)
	fmt.Printf("[DEBUG] Analyze: totalJobs = %d\n", atomic.LoadInt64(&totalJobs))
	go func() {
		defer func() {
			fmt.Printf("[DEBUG] Initial job: defer wg.Done() called, totalJobs before done = %d\n", atomic.LoadInt64(&totalJobs))
			wg.Done()
		}()
		fmt.Println("[DEBUG] Initial job: goroutine started")
		select {
		case jobs <- Job{
			URL:       opts.URL,
			ParentURL: opts.URL,
			Depth:     int(opts.Depth),
		}:
			fmt.Println("[DEBUG] Initial job: sent to channel")
		case <-ctx.Done():
			fmt.Println("[DEBUG] Initial job: context cancelled")
		}
	}()

	go func() {
		fmt.Println("[DEBUG] Jobs closer: waiting for wg...")
		wg.Wait()
		fmt.Println("[DEBUG] Jobs closer: wg.Wait() done, closing jobs channel")
		close(jobs)
		fmt.Println("[DEBUG] Jobs closer: jobs channel closed")
	}()

	go func() {
		fmt.Println("[DEBUG] Results closer: waiting for wg...")
		wg.Wait()
		fmt.Println("[DEBUG] Results closer: wg.Wait() done, closing results channel")
		close(results)
		fmt.Println("[DEBUG] Results closer: results channel closed")
	}()

	fmt.Println("[DEBUG] Analyze: starting reporter")
	reporter := NewReporter(opts, normalizedRoot)
	fmt.Println("[DEBUG] Analyze: calling ProcessResults")
	reporter.ProcessResults(results)
	fmt.Println("[DEBUG] Analyze: ProcessResults finished")

	fmt.Println("[DEBUG] Analyze: calling BuildFinalJSON")
	return reporter.BuildFinalJSON()
}
func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	wg *sync.WaitGroup,
	opts Options,
) {
	fmt.Printf("[DEBUG] Worker %d: started\n", id)

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	for job := range jobs {
		fmt.Printf("[DEBUG] Worker %d: got job %s (depth=%d)\n", id, job.URL, job.Depth)

		if job.Depth < 0 {
			fmt.Printf("[DEBUG] Worker %d: depth < 0, skipping\n", id)
			continue
		}

		if err := waitForRateLimit(ctx); err != nil {
			fmt.Printf("[DEBUG] Worker %d: rate limit error: %v\n", id, err)
			continue
		}

		fmt.Printf("[DEBUG] Worker %d: fetching %s\n", id, job.URL)
		html, resp, respStatus, errResp := fetchWithRetry(ctx, job.URL, opts, rng, id, false)

		if errResp != nil {
			fmt.Printf("[DEBUG] Worker %d: fetch error: %v\n", id, errResp)
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
				fmt.Printf("[DEBUG] Worker %d: sent error result\n", id)
			case <-ctx.Done():
				fmt.Printf("[DEBUG] Worker %d: context cancelled\n", id)
			}
			continue
		}

		if resp == nil {
			fmt.Printf("[DEBUG] Worker %d: resp is nil\n", id)
			errMsg := "response is nil"
			select {
			case results <- Link{
				URL:              job.URL,
				Error:            errMsg,
				ParentURL:        job.ParentURL,
				ParentStatusCode: 0,
				ParentStatus:     "",
			}:
				fmt.Printf("[DEBUG] Worker %d: sent nil result\n", id)
			case <-ctx.Done():
				fmt.Printf("[DEBUG] Worker %d: context cancelled\n", id)
			}
			continue
		}

		if resp.StatusCode >= 400 {
			fmt.Printf("[DEBUG] Worker %d: HTTP error %d\n", id, resp.StatusCode)
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
				fmt.Printf("[DEBUG] Worker %d: sent HTTP error result\n", id)
			case <-ctx.Done():
				fmt.Printf("[DEBUG] Worker %d: context cancelled\n", id)
			}
			continue
		}

		if resp.Body != nil {
			fmt.Printf("[DEBUG] Worker %d: reading body\n", id)
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				html = string(body)
			}
			resp.Body.Close()
		}

		if !IsSameDomain(job.URL, opts.URL) {
			fmt.Printf("[DEBUG] Worker %d: different domain, skipping\n", id)
			continue
		}

		contentType := resp.Header.Get("Content-Type")
		fmt.Printf("[DEBUG] Worker %d: Content-Type: %s\n", id, contentType)

		var seo SEO
		var assets []Asset

		switch {
		case strings.Contains(contentType, "text/html"):
			fmt.Printf("[DEBUG] Worker %d: parsing HTML\n", id)
			seo = getSeoFromHtml(html)
			assets = extractAssetsFromHtml(html, job.URL, opts, ctx, rng, id)
		case strings.Contains(contentType, "application/rss+xml"),
			strings.Contains(contentType, "application/atom+xml"),
			strings.Contains(contentType, "text/xml"):
			fmt.Printf("[DEBUG] Worker %d: parsing XML\n", id)
			seo = getSeoFromXml(html)
			assets = []Asset{}
		default:
			fmt.Printf("[DEBUG] Worker %d: non-HTML/XML\n", id)
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
			fmt.Printf("[DEBUG] Worker %d: extracting links (depth=%d)\n", id, job.Depth)
			links := getLinksFromHtml(html, job.URL, opts)
			fmt.Printf("[DEBUG] Worker %d: found %d links\n", id, len(links))

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
						fmt.Printf("[DEBUG] Worker %d: new link: %s\n", id, validatedLink)

						wg.Add(1)
						atomic.AddInt64(&totalJobs, 1)
						fmt.Printf("[DEBUG] Worker %d: wg.Add(1), totalJobs = %d\n", id, atomic.LoadInt64(&totalJobs))

						go func(linkURL, parentURL string, parentCode int, parentStatus string, depth int) {
							defer func() {
								fmt.Printf("[DEBUG] Worker %d: goroutine for %s calling wg.Done(), totalJobs before done = %d\n", id, linkURL, atomic.LoadInt64(&totalJobs))
								wg.Done()
								atomic.AddInt64(&totalJobs, -1)
								fmt.Printf("[DEBUG] Worker %d: goroutine for %s wg.Done() called, totalJobs = %d\n", id, linkURL, atomic.LoadInt64(&totalJobs))
							}()
							fmt.Printf("[DEBUG] Worker %d: goroutine for %s started\n", id, linkURL)
							select {
							case jobs <- Job{
								URL:              linkURL,
								ParentURL:        parentURL,
								ParentStatusCode: parentCode,
								ParentStatus:     parentStatus,
								Depth:            depth,
							}:
								fmt.Printf("[DEBUG] Worker %d: sent job for %s\n", id, linkURL)
							case <-ctx.Done():
								fmt.Printf("[DEBUG] Worker %d: context cancelled for %s\n", id, linkURL)
							}
							fmt.Printf("[DEBUG] Worker %d: goroutine for %s finishing\n", id, linkURL)
						}(validatedLink, job.URL, resp.StatusCode, respStatus, job.Depth-1)
					} else {
						visitedMu.Unlock()
					}
				}
			}
		}

		fmt.Printf("[DEBUG] Worker %d: sending result for %s\n", id, job.URL)
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
			fmt.Printf("[DEBUG] Worker %d: result sent\n", id)
		case <-ctx.Done():
			fmt.Printf("[DEBUG] Worker %d: context cancelled\n", id)
			return
		}

		// ВАЖНО: уменьшаем счетчик wg после обработки job'а
		wg.Done()
		fmt.Printf("[DEBUG] Worker %d: wg.Done() after job %s, totalJobs = %d\n", id, job.URL, atomic.LoadInt64(&totalJobs))
	}

	fmt.Printf("[DEBUG] Worker %d: exiting (jobs channel closed)\n", id)
}
*/

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	setupRateLimiter(opts)

	workersCount := opts.Concurrency
	if workersCount <= 0 {
		workersCount = 4
	}

	jobs := make(chan Job, 200)
	results := make(chan Link, 200)

	var wg sync.WaitGroup

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &wg, opts)
		}(i)
	}

	normalizedRoot, _ := NormalizeURL(opts.URL, opts.URL)

	visitedMu.Lock()
	visited = make(map[string]struct{})
	visitedMu.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case jobs <- Job{
			URL:       opts.URL,
			ParentURL: opts.URL,
			Depth:     int(opts.Depth),
		}:
		case <-ctx.Done():
		}
	}()

	go func() {
		wg.Wait()
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	reporter := NewReporter(opts, normalizedRoot)
	reporter.ProcessResults(results)

	return reporter.BuildFinalJSON()
}
