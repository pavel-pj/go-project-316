package crawler

import (
	"context"
	"io"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"
)

// worker - функция-воркер, которая обрабатывает задачи из канала jobs,
// выполняет HTTP-запросы, парсит HTML и отправляет результаты в канал results.
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
			// парсим html
			html, resp, respStatus, errResp := fetchWithRetry(ctx, currentJob.URL, opts, rng, id, false)

			// Пишем ошибку
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

			// нулевой ответ
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

			// ошибка статуса
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
			// Не идем в чужой домен
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
				// Получаем ссылки
				links := getLinksFromHtml(html, currentJob.URL, opts)
				for _, link := range links {
					validatedLink, err := NormalizeURL(link, opts.URL)
					if err != nil {
						continue
					}
					if validatedLink == normalizedRoot {
						continue
					}
					// Добавляем новую ссылку в работу
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
			// пищем в результат
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
		// Уменьшаем счетчик работы
		atomic.AddInt64(pendingJobs, -1)
	}
}
