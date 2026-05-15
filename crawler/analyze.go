package crawler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Analyze - главная функция, которая запускает процесс обхода сайта,
// управляет воркерами и возвращает отчет в формате JSON.
func Analyze(ctx context.Context, opts Options) ([]byte, error) {

	setupRateLimiter(opts)

	workersCount := opts.Concurrency
	if workersCount <= 0 {
		workersCount = 4
	}

	jobs := make(chan Job, 200)
	results := make(chan Link, 200)

	// wg используется для синхронизации worker
	var wg sync.WaitGroup
	// счетчик Jobs
	var pendingJobs int64

	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &pendingJobs, opts)
		}(i)
	}
	// Нормальизуем путь главной страницы
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

	// Ждем заввершения Jobs
	go func() {
		for {
			if atomic.LoadInt64(&pendingJobs) == 0 {
				close(jobs)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Завершение Workers
	go func() {
		wg.Wait()
		close(results)
	}()

	// Отчеты
	reporter := NewReporter(opts, normalizedRoot)
	reporter.ProcessResults(results)

	return reporter.BuildFinalJSON()
}
