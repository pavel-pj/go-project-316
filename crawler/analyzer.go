package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Analyze главная функция анализа
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	SetupRateLimiter(opts)

	// Инициализация
	visited = make(map[string]struct{})
	assetsCache = make(map[string]Asset)

	normalizedRoot, _ := NewParser().NormalizeURL(opts.URL, opts.URL)
	MarkVisited(normalizedRoot)

	// Создание каналов
	results := make(chan Link, ResultQueueSize)

	// Запуск воркеров
	workerPool := NewWorkerPool(opts, results)
	workerPool.Start(ctx)

	// Отправка начальной задачи
	workerPool.AddJob(Job{
		URL:       opts.URL,
		ParentURL: opts.URL,
		Depth:     int(opts.Depth),
	})

	// Закрытие каналов
	go func() {
		workerPool.Wait()
		close(results)
	}()

	// Сбор результатов
	collector := NewResultCollector(opts.URL, opts.Depth)
	for result := range results {
		collector.Process(result, normalizedRoot)
	}

	collector.ProcessBrokenLinks()

	// Формирование отчета
	report := Report{
		RootURL:     opts.URL,
		Depth:       opts.Depth,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Pages:       collector.GetPages(),
	}

	// Маршалинг JSON
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
