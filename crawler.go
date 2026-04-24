package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

type Options struct {
	URL        string
	Depth      int32
	HTTPClient *http.Client
}

type Page struct {
	URL          string `json:"url"`
	Depth        int    `json:"depth"`
	HttpStatus   int    `json:"http_status"`
	Status       string `json:"status"`
	BrokenLinks  []Link `json:"broken_links"`
	DiscoveredAt string `json:"discovered_at"`
}

type Link struct {
	URL          string  `json:"url"`
	StatusCode   *int    `json:"status_code,omitempty"`
	Error        *string `json:"error,omitempty"`
	ParentURL    *string `json:"parent_url,omitempty"`
	ParentStatus *int    `json:"parent_http_status,omitempty"`
}

var (
	visited   = make(map[string]struct{})
	visitedMu sync.RWMutex
)

type Job struct {
	URL          string
	ParentURL    string
	ParentStatus int
}

// Расширенный список User-Agent как в рабочем примере
var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Edge/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
}

func getRandomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return userAgents[rng.Intn(len(userAgents))]
}

// Генерация случайного IP для X-Forwarded-For
func getRandomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(255),
		rand.Intn(255),
		rand.Intn(255),
		rand.Intn(255))
}

// Генерация случайной задержки для воркеров (миллисекунды)
func getRandomWorkerDelay() time.Duration {
	// От 1 до 25 секунд как в оригинале, но теперь в диапазоне 3-10 секунд
	//delay := rand.Intn(2000) + 3000 // 3-10 секунд в миллисекундах
	delay := rand.Intn(1) + 1 // 3-10 секунд в миллисекундах
	return time.Duration(delay) * time.Millisecond
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	jobs := make(chan Job, 200)
	results := make(chan Link, 200)

	var wg sync.WaitGroup
	var jobWg sync.WaitGroup

	// Запускаем воркеров (можно увеличить количество)
	workersCount := 5 // Измените на нужное количество воркеров
	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &jobWg, opts)
		}(i)
	}

	// Добавляем корневую задачу
	jobWg.Add(1)
	jobs <- Job{
		URL:       opts.URL,
		ParentURL: opts.URL,
	}

	// Мониторинг завершения - закрываем jobs когда все задачи выполнены
	go func() {
		jobWg.Wait()
		fmt.Println("Все задачи выполнены, закрываем jobs")
		close(jobs)
		fmt.Println("ЗАКРЫЛИ jobs")
	}()

	// Закрываем results после того, как все воркеры завершились
	go func() {
		wg.Wait()
		fmt.Println("Все воркеры завершились, закрываем results")
		close(results)
	}()

	// Собираем результаты
	var brokenLinks []Link

	for result := range results {
		if result.Error != nil {
			brokenLinks = append(brokenLinks, result)
			continue
		}

		// Проверяем статус код
		if result.StatusCode != nil {
			statusCode := *result.StatusCode
			// Добавляем только если статус >= 400 (ошибка клиента/сервера)
			// Или если статус < 200 (информационные ответы)
			if statusCode >= 400 {
				brokenLinks = append(brokenLinks, result)
			}
		}
	}

	/*
		var pages []Page
		for result := range results {

			// если нет такой страницы в Pages - добавляем
			isPageExists := false
			for _, page := range pages {
				if page.URL == *result.ParentURL {
					isPageExists = true
				}
			}

			if !isPageExists {
				pages = append(pages, Page{
					URL:   *result.ParentURL,
					Depth: 1,
				})
			}

			if result.Error != nil {
				brokenLinks = append(brokenLinks, result)
				continue
			}

			// Проверяем статус код
			if result.StatusCode != nil {
				statusCode := *result.StatusCode
				// Добавляем только если статус >= 400 (ошибка клиента/сервера)
				// Или если статус < 200 (информационные ответы)
				if statusCode >= 400 {
					brokenLinks = append(brokenLinks, result)
				}
			}
		}
	*/

	//fmt.Printf("Закрыли workres\n")
	//fmt.Println(data)

	//fmt.Printf("Собрано результатов: %d\n", len(data))
	//return json.Marshal(brokenLinks)
	return json.MarshalIndent(brokenLinks, "", "  ")
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Link,
	jobWg *sync.WaitGroup,
	opts Options,
) {

	// Создаем локальный генератор случайных чисел для каждого воркера
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for job := range jobs {
		//fmt.Printf("Worker %d: начал обработку %s\n", id, job.URL)

		// Используем задержку как в рабочем примере (3-10 секунд)
		delay := getRandomWorkerDelay()
		//fmt.Printf("Worker %d: жду %v перед запросом к %s\n", id, delay, job.URL)

		func() {
			select {
			case <-time.After(delay):
				html, resp, errResp := parseHtml(ctx, job.URL, opts, rng)
				if errResp != nil {
					fmt.Printf("Worker %d: ошибка при запросе %s: %v\n", id, job.URL, errResp)
					// Отправляем результат с ошибкой
					errMsg := errResp.Error()
					select {
					case results <- Link{
						URL:          job.URL,
						Error:        &errMsg,
						ParentURL:    &job.ParentURL,
						ParentStatus: &job.ParentStatus,
					}:
					case <-ctx.Done():
					}

					return

				}

				if resp == nil {
					fmt.Printf("Worker %d: resp is nil for %s\n", id, job.URL)
					fmt.Printf("Worker %d: resp is nil for %s\n", id, job.URL)
					errMsg := "response is nil"
					select {
					case results <- Link{
						URL:          job.URL,
						Error:        &errMsg,
						ParentURL:    &job.ParentURL,
						ParentStatus: &resp.StatusCode,
					}:
					case <-ctx.Done():
					}

					return

				}

				fmt.Printf("Worker %d: ОТВЕТ %s: %s\n", id, job.URL, resp.Status)

				links := getLinksFromHtml(html, job.URL, opts)
				//fmt.Printf("Worker %d: нашел %d ссылок на %s\n", id, len(links), job.URL)

				// Добавляем новые задачи

				// Добавляем новые задачи
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
								URL:          validatedLink,
								ParentURL:    job.URL,
								ParentStatus: resp.StatusCode,
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
				//fmt.Printf("Worker %d: добавил %d новых задач\n", id, newJobsCount)

				select {
				case results <- Link{
					URL:          job.URL,
					StatusCode:   &resp.StatusCode,
					ParentURL:    &job.ParentURL,
					ParentStatus: &job.ParentStatus,
				}:
					//fmt.Printf("Worker %d: отправил результат для %s\n", id, job.URL)
				case <-ctx.Done():
					jobWg.Done()
					return
				}

			case <-ctx.Done():
				jobWg.Done()
				return
			}
		}()

		jobWg.Done()
		//fmt.Printf("Worker %d: закончил обработку %s\n", id, job.URL)
	}

	//fmt.Printf("Worker %d: вышел из цикла jobs\n", id)
}

func isNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
}

func parseHtml(ctx context.Context, link string, opts Options, rng *rand.Rand) (string, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, fmt.Errorf("cant prepare request to url:%s, %w", link, err)
	}

	// Устанавливаем случайный User-Agent
	randomUA := getRandomUserAgent()
	req.Header.Set("User-Agent", randomUA)

	// Устанавливаем все заголовки как в рабочем примере

	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	//req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")

	// Добавляем случайный заголовок X-Forwarded-For
	randomIP := getRandomIP()
	req.Header.Set("X-Forwarded-For", randomIP)

	// Дополнительные заголовки для более реалистичного поведения
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")

	// Добавляем Referer для некоторых запросов (с вероятностью 70%)
	if rng.Float64() < 0.7 {
		referer := fmt.Sprintf("https://%s/", strings.Split(link, "/")[2])
		req.Header.Set("Referer", referer)
	}

	//fmt.Printf("Запрос к %s: User-Agent=%s, X-Forwarded-For=%s\n",link, randomUA[:50]+"...", randomIP)

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("cant handle request to url:%s, %w", link, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("cant read response body from url:%s, %w", link, err)
	}

	return string(body), resp, nil
}

func getLinksFromHtml(htmlBody string, baseURL string, opts Options) []string {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return []string{}
	}

	var links []string

	var findLinks func(*html.Node)
	findLinks = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					normalized, err := NormalizeURL(attr.Val, baseURL)
					if err != nil {
						continue
					}
					links = append(links, normalized)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findLinks(c)
		}
	}

	findLinks(doc)
	return links
}

func NormalizeURL(href, baseURL string) (string, error) {

	if strings.HasPrefix(href, "#") {
		return "", fmt.Errorf("skip anchor: %s", href)
	}

	if href == "" {
		return "", fmt.Errorf("empty href")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	parsed, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid href: %w", err)
	}

	resolved := base.ResolveReference(parsed)

	_, err = url.ParseRequestURI(resolved.String())
	if err != nil {
		return "", fmt.Errorf("invalid resolved URL: %w", err)
	}

	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme: %s (only http/https allowed)", resolved.Scheme)
	}

	if resolved.Host == "" {
		return "", fmt.Errorf("no host in URL: %s", resolved.String())
	}

	return resolved.String(), nil
}
