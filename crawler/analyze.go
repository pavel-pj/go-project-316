package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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

	reporter := NewReporter(opts, normalizedRoot)
	reporter.ProcessResults(results)

	report := reporter.BuildFinalReport()

	var jsonResult []byte
	var err error

	if opts.IndentJSON {
		jsonResult, err = json.MarshalIndent(report, "", "  ")
	} else {
		jsonResult, err = json.Marshal(report)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return jsonResult, nil
}
