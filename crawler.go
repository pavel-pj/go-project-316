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

type Result struct {
	URL        string `json:"url"`
	StatusCode string `json:"status_code"`
}

var (
	visited   = make(map[string]struct{})
	visitedMu sync.RWMutex
)

type Job struct {
	URL string
}

// РАСШИРЕННЫЙ список User-Agent (как в рабочем примере - 8 штук)
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

// НОВАЯ ФУНКЦИЯ: генерация случайного IP
func getRandomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d", rand.Intn(255), rand.Intn(255), rand.Intn(255), rand.Intn(255))
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	jobs := make(chan Job, 150)
	results := make(chan Result, 150)

	var wg sync.WaitGroup
	var jobWg sync.WaitGroup

	// Запускаем воркеров
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &jobWg, opts)
		}(i)
	}

	// Добавляем корневую задачу
	jobWg.Add(1)
	jobs <- Job{URL: opts.URL}

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
	var data []Result
	for result := range results {
		data = append(data, result)
	}

	fmt.Printf("Закрыли workres")
	fmt.Println(data)
	// Ждем завершения всех воркеров
	//wg.Wait()
	//close(results)

	fmt.Printf("Собрано результатов: %d\n", len(data))
	return json.Marshal(data)
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job, // меняем на двунаправленный канал
	results chan<- Result,
	jobWg *sync.WaitGroup,
	opts Options,
) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for job := range jobs {
		//fmt.Printf("Worker %d: начал обработку %s\n", id, job.URL)

		// ИЗМЕНЕНО: задержка как в рабочем примере от 3 до 10 секунд
		delay := time.Duration(rng.Intn(3)+4) * time.Second
		//fmt.Printf("Worker %d: жду %v перед запросом к %s\n", id, delay, job.URL)

		func() {
			select {
			case <-time.After(delay):
				html, resp, err := parseHtml(ctx, job.URL, opts)
				if err != nil {
					fmt.Printf("Worker %d: ошибка при запросе %s: %v\n", id, job.URL, err)
					jobWg.Done()
					return
				}

				if resp == nil {
					fmt.Printf("Worker %d: resp is nil for %s\n", id, job.URL)
					jobWg.Done()
					return
				}

				fmt.Printf("Worker %d: ОТВЕТ %s: %s\n", id, job.URL, resp.Status)

				links := getLinksFromHtml(html, job.URL, opts)
				//fmt.Printf("Worker %d: нашел %d ссылок на %s\n", id, len(links), job.URL)

				// Добавляем новые задачи
				newJobsCount := 0
				for _, link := range links {
					if isNewLink(link) {
						validatedLink, err := NormalizeURL(link, opts.URL)
						if err != nil {
							continue
						}
						visitedMu.Lock()
						visited[link] = struct{}{}
						visitedMu.Unlock()

						jobWg.Add(1)
						newJobsCount++
						select {
						case jobs <- Job{URL: validatedLink}:
							//fmt.Printf("Worker %d: добавил задачу: %s\n", id, validatedLink)
						case <-ctx.Done():
							jobWg.Done()
							return
						}
					}
				}
				//fmt.Printf("Worker %d: добавил %d новых задач\n", id, newJobsCount)

				select {
				case results <- Result{
					URL:        job.URL,
					StatusCode: resp.Status,
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

func parseHtml(ctx context.Context, link string, opts Options) (string, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, fmt.Errorf("cant prepare request to url:%s, %w", link, err)
	}

	// ИЗМЕНЕНО: полный набор заголовков как в рабочем примере
	randomUA := getRandomUserAgent()
	req.Header.Set("User-Agent", randomUA)

	// ВСЕ заголовки из рабочего примера
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	//req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")

	// ДОБАВЛЕНО: случайный X-Forwarded-For
	randomIP := getRandomIP()
	req.Header.Set("X-Forwarded-For", randomIP)

	// ОСТАВЛЯЕМ существующие заголовки
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")

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

/*

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

type Result struct {
	URL        string `json:"url"`
	StatusCode string `json:"status_code"`
}

var (
	visited   = make(map[string]struct{})
	visitedMu sync.RWMutex
)

type Job struct {
	URL string
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

// Генерация случайной задержки от 3 до 10 секунд (как в рабочем примере)
func getRandomDelay() time.Duration {
	delay := rand.Intn(8) + 3 // 3-10 секунд
	return time.Duration(delay) * time.Second
}

// Генерация случайной задержки для воркеров (миллисекунды)
func getRandomWorkerDelay() time.Duration {
	// От 1 до 25 секунд как в оригинале, но теперь в диапазоне 3-10 секунд
	delay := rand.Intn(8000) + 3000 // 3-10 секунд в миллисекундах
	return time.Duration(delay) * time.Millisecond
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	jobs := make(chan Job, 50)
	results := make(chan Result, 50)

	var wg sync.WaitGroup
	var jobWg sync.WaitGroup

	// Запускаем воркеров (можно увеличить количество)
	workersCount := 3 // Измените на нужное количество воркеров
	for i := 0; i < workersCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &jobWg, opts)
		}(i)
	}

	// Добавляем корневую задачу
	jobWg.Add(1)
	jobs <- Job{URL: opts.URL}

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
	var data []Result
	for result := range results {
		data = append(data, result)
	}

	fmt.Printf("Закрыли workres\n")
	fmt.Println(data)

	fmt.Printf("Собрано результатов: %d\n", len(data))
	return json.Marshal(data)
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job,
	results chan<- Result,
	jobWg *sync.WaitGroup,
	opts Options,
) {
	// Создаем локальный генератор случайных чисел для каждого воркера
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for job := range jobs {
		fmt.Printf("Worker %d: начал обработку %s\n", id, job.URL)

		// Используем задержку как в рабочем примере (3-10 секунд)
		delay := getRandomWorkerDelay()
		fmt.Printf("Worker %d: жду %v перед запросом к %s\n", id, delay, job.URL)

		func() {
			select {
			case <-time.After(delay):
				html, resp, err := parseHtml(ctx, job.URL, opts, rng)
				if err != nil {
					fmt.Printf("Worker %d: ошибка при запросе %s: %v\n", id, job.URL, err)
					jobWg.Done()
					return
				}

				if resp == nil {
					fmt.Printf("Worker %d: resp is nil for %s\n", id, job.URL)
					jobWg.Done()
					return
				}

				fmt.Printf("Worker %d: ОТВЕТ %s: %s\n", id, job.URL, resp.Status)

				links := getLinksFromHtml(html, job.URL, opts)
				fmt.Printf("Worker %d: нашел %d ссылок на %s\n", id, len(links), job.URL)

				// Добавляем новые задачи
				newJobsCount := 0
				for _, link := range links {
					if isNewLink(link) {
						validatedLink, err := NormalizeURL(link, opts.URL)
						if err != nil {
							continue
						}
						visitedMu.Lock()
						visited[link] = struct{}{}
						visitedMu.Unlock()

						jobWg.Add(1)
						newJobsCount++
						select {
						case jobs <- Job{URL: validatedLink}:
							fmt.Printf("Worker %d: добавил задачу: %s\n", id, validatedLink)
						case <-ctx.Done():
							jobWg.Done()
							return
						}
					}
				}
				fmt.Printf("Worker %d: добавил %d новых задач\n", id, newJobsCount)

				select {
				case results <- Result{
					URL:        job.URL,
					StatusCode: resp.Status,
				}:
					fmt.Printf("Worker %d: отправил результат для %s\n", id, job.URL)
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
		fmt.Printf("Worker %d: закончил обработку %s\n", id, job.URL)
	}

	fmt.Printf("Worker %d: вышел из цикла jobs\n", id)
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
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
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

	fmt.Printf("Запрос к %s: User-Agent=%s, X-Forwarded-For=%s\n",
		link, randomUA[:50]+"...", randomIP)

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

/*
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

type Result struct {
	URL        string `json:"url"`
	StatusCode string `json:"status_code"`
}

var (
	visited   = make(map[string]struct{})
	visitedMu sync.RWMutex
)

type Job struct {
	URL string
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
}

func getRandomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return userAgents[rng.Intn(len(userAgents))]
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	jobs := make(chan Job, 50)
	results := make(chan Result, 50)

	var wg sync.WaitGroup
	var jobWg sync.WaitGroup

	// Запускаем воркеров
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, ctx, jobs, results, &jobWg, opts)
		}(i)
	}

	// Добавляем корневую задачу
	jobWg.Add(1)
	jobs <- Job{URL: opts.URL}

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
	var data []Result
	for result := range results {
		data = append(data, result)
	}

	fmt.Printf("Закрыли workres")
	fmt.Println(data)
	// Ждем завершения всех воркеров
	//wg.Wait()
	//close(results)

	fmt.Printf("Собрано результатов: %d\n", len(data))
	return json.Marshal(data)
}

func worker(
	id int,
	ctx context.Context,
	jobs chan Job, // меняем на двунаправленный канал
	results chan<- Result,
	jobWg *sync.WaitGroup,
	opts Options,
) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	for job := range jobs {
		fmt.Printf("Worker %d: начал обработку %s\n", id, job.URL)

		delay := time.Duration(rng.Intn(25000)+1000) * time.Millisecond
		fmt.Printf("Worker %d: жду %v перед запросом к %s\n", id, delay, job.URL)

		func() {
			select {
			case <-time.After(delay):
				html, resp, err := parseHtml(ctx, job.URL, opts)
				if err != nil {
					fmt.Printf("Worker %d: ошибка при запросе %s: %v\n", id, job.URL, err)
					jobWg.Done()
					return
				}

				if resp == nil {
					fmt.Printf("Worker %d: resp is nil for %s\n", id, job.URL)
					jobWg.Done()
					return
				}

				fmt.Printf("Worker %d: ОТВЕТ %s: %s\n", id, job.URL, resp.Status)

				links := getLinksFromHtml(html, job.URL, opts)
				fmt.Printf("Worker %d: нашел %d ссылок на %s\n", id, len(links), job.URL)

				// Добавляем новые задачи
				newJobsCount := 0
				for _, link := range links {
					if isNewLink(link) {
						validatedLink, err := NormalizeURL(link, opts.URL)
						if err != nil {
							continue
						}
						visitedMu.Lock()
						visited[link] = struct{}{}
						visitedMu.Unlock()

						jobWg.Add(1)
						newJobsCount++
						select {
						case jobs <- Job{URL: validatedLink}:
							fmt.Printf("Worker %d: добавил задачу: %s\n", id, validatedLink)
						case <-ctx.Done():
							jobWg.Done()
							return
						}
					}
				}
				fmt.Printf("Worker %d: добавил %d новых задач\n", id, newJobsCount)

				select {
				case results <- Result{
					URL:        job.URL,
					StatusCode: resp.Status,
				}:
					fmt.Printf("Worker %d: отправил результат для %s\n", id, job.URL)
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
		fmt.Printf("Worker %d: закончил обработку %s\n", id, job.URL)
	}

	fmt.Printf("Worker %d: вышел из цикла jobs\n", id)
}

func isNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
}

func parseHtml(ctx context.Context, link string, opts Options) (string, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, fmt.Errorf("cant prepare request to url:%s, %w", link, err)
	}

	req.Header.Set("User-Agent", getRandomUserAgent())

	// Добавляем другие заголовки как у реального браузера

	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	//req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")

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
*/

/*package crawler

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
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

type Options struct {
	URL        string
	Depth      int32
	HTTPClient *http.Client
}

type Response struct {
	URL         string `json:"url"`
	Http_status string `json:"http_status"`
}

type Link struct {
	StatusCode int32
	Error      string
	Depth      int32 // На какой глубине найден
}

var (
	//links_list = make(map[string]struct{})
	//pending = make(map[string]struct{})
	visited = make(map[string]struct{})

	// Мьютексы для конкурентного доступа
	//links_listMu sync.RWMutex
	visitedMu sync.RWMutex
	//pendingMu sync.RWMutex
)

type Job struct {
	URL string
	//Depth int32
}

type Result struct {
	URL        string `json:"url"`
	StatusCode string `json:"status_code"`
	// Depth     int32  `json:"depth"`
	// Error     string `json:"error,omitempty"`
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {

	jobs := make(chan Job, 50)
	results := make(chan Result, 50)
	newLinks := make(chan string, 50)

	var wg sync.WaitGroup
	var pending int32 // счетчик ожидающих задач

	// запускаем горутины
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			//fmt.Printf("Analyze: запускаю worker %d с каналом jobs %p\n", id, jobs) // ← ДОБАВИТЬ
			worker(id, ctx, jobs, results, newLinks, opts, &pending)
		}(i)

	}

	// запускаем задачи.
	go dispatchNewLink(ctx, jobs, newLinks, opts, &pending)

	// инициализируем корневой URL
	atomic.AddInt32(&pending, 1)
	fmt.Printf("Количество задач_НАЧАЛО: %d\n", atomic.LoadInt32(&pending))
	newLinks <- opts.URL

	// Мониторинг завершения
	go func() {
		for {
			time.Sleep(1000 * time.Millisecond)
			fmt.Printf("Мониторинг завершения : количествво задач, %d\n", atomic.LoadInt32(&pending))

			p := atomic.LoadInt32(&pending)
			//fmt.Printf("Ожидает обработки: %d\n", p)
			if p == 0 {
				time.Sleep(500 * time.Millisecond)
				fmt.Println("Все задачи выполнены, закрываем каналы")
				close(newLinks)
				close(jobs)
				close(results)
				return
			}
		}
	}()

	var data []Result

	for result := range results {
		data = append(data, result)
	}

	return json.Marshal(data)

}

func worker(
	id int,
	ctx context.Context,
	jobs <-chan Job,
	results chan<- Result,
	newLinks chan<- string,
	opts Options,
	pending *int32,

) {

	// Создаем свой генератор случайных чисел для каждого воркера
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
	for job := range jobs {

		delay := time.Duration(rng.Intn(1)+1000) * time.Millisecond
		fmt.Printf("Worker %d: жду %v перед запросом к %s\n", id, delay, job.URL)

		var html string
		var resp *http.Response
		var err error

		select {
		case <-time.After(delay):
			// задержка прошла
			html, resp, err = parseHtml(ctx, job.URL, opts)
			if err != nil {
				fmt.Printf("Cant prepare request to url:%s,  %w\n", job.URL, err)
				continue

			}
		case <-ctx.Done():
			atomic.AddInt32(pending, -1)
			return
		}

		fmt.Printf("ОТВЕТ СЕРВЕРА: %s\n", resp.Status)
		//fmt.Printf("html:")
		//fmt.Printf(html)
		links := getLinksFromHtml(html, job.URL, opts)
		//fmt.Printf("Получил ссылок:")
		//fmt.Println(links)
		// Добавляем новые задачи
		for _, link := range links {

			//if isNewLink(link) {

			//fmt.Printf("Количество задач_worker_добавление новой задачи: %d\n", atomic.LoadInt32(pending))
			select {
			case newLinks <- link:

				//fmt.Printf("Worker %d: добавил ссылку в канал newLinks: %s\n", id, link)
			case <-ctx.Done():
				fmt.Println("ОШИБКА ВОРКЕРА")
				//fmt.Printf("Количество задач_worker_ctx.Done: %d\n", atomic.LoadInt32(pending))

				return
			}
			//}
		}

		//response := Response{
		//	URL:         opts.URL,
		//	Http_status: resp.Status,
		//}
		result := Result{
			URL:        job.URL,
			StatusCode: resp.Status,
		}
		//fmt.Printf("Worker %d: прочитал один job, result: %s\n", id, result) // ← ДОБАВИТЬ
		results <- result

		atomic.AddInt32(pending, -1)
		fmt.Printf("-pedning: %d, Worker %d: закончил обработку %s, pending=%d\n", atomic.LoadInt32(pending), id, job.URL, atomic.LoadInt32(pending))

	}

	//fmt.Printf("Worker %d: завершил чтение из jobs\n", id) // ← ДОБАВИТЬ

}

func dispatchNewLink(
	ctx context.Context,
	jobs chan<- Job,
	newLinks <-chan string,
	opts Options,
	pending *int32,

) {
	//fmt.Printf("Мы в Dispatch\n")
	for {
		select {
		case <-ctx.Done():
			return
		case link, ok := <-newLinks:
			//fmt.Printf("Проверка в Dispatch newLink: %s\n", link)
			//Завершаем если канал закрыт
			if !ok {
				return
			}
			isNewLink := isNewLink(link)
			validatedLink, err := NormalizeURL(link, opts.URL)
			if err != nil {
				continue
			}
			//Добавляем ссылку в JOB
			//fmt.Printf("isNewLink: %t, err: %v, validated:%s\n", isNewLink, err, validatedLink)

			if isNewLink {

				visitedMu.Lock()
				visited[link] = struct{}{}
				visitedMu.Unlock()

				select {
				case jobs <- Job{URL: validatedLink}:
					atomic.AddInt32(pending, 1)
					//if link == "https://www.kamin-100.ru/krsk/foto/bbq.html" {
					fmt.Printf("+pending: %d,Dispatch: отправил в jobs: %s\n", atomic.LoadInt32(pending), link)
					//}
				case <-ctx.Done():
					atomic.AddInt32(pending, -1)
					//fmt.Printf("Dispatch ctx.DONE: %d\n", atomic.LoadInt32(pending))
					return
				}
			} else {
				//fmt.Printf("Dispatch УЖЕ СУЩЕСТВУЕТ ТАКАЯ ССЫЛКА: %d\n", atomic.LoadInt32(pending))

			}
		}
	}
}

func isNewLink(link string) bool {
	isNewLink := true

	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()

	//if link == "https://www.kamin-100.ru" {
	//	fmt.Printf("ПРОВЕРЯЕМ kamin-100\n")
	//}

	if ok {
		// Если ссылка существует
		//fmt.Printf("KAMIN-100 НЕ СУЩЕСТВУЕТ \n")
		//fmt.Printf("Вывожу все текущие ссылки \n")

		isNewLink = false
	}

	//if link == "https://www.kamin-100.ru/krsk/foto/bbq.html" {
	//	fmt.Printf("func isNewLink. link:%s, isNewLink:%t\n", link, isNewLink)
	//}

	//fmt.Printf("func isNewLink. link:%s, isNewLink:%t\n", link, isNewLink)
	//if link == "https://www.kamin-100.ru" && isNewLink == false {
	//	fmt.Printf("kamin-100 УЖЕ РАНЕЕ БЫЛ ДОБАВЛЕН, isNewLink ==false\n")
	//}

	//if link == "https://www.kamin-100.ru" && isNewLink == true {
	//	fmt.Printf("kamin-100 НЕ БЫЛ ДОБАВЛЕН, isNewLink ==true\n")
	//	fmt.Printf("Вывожу все текущие ссылки \n")
	//	fmt.Println(visited)
	//}
	//fmt.Println(visited)
	return isNewLink
}

// получает html страницы
func parseHtml(ctx context.Context, link string, opts Options) (string, *http.Response, error) {

	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, fmt.Errorf("Cant prepare request to url:%s,  %w", link, err)
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("Cant hadle request to url:%s,  %w", link, err)
	}

	defer resp.Body.Close()

	// Читаем тело ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("Cant read response body from url:%s, %w", opts.URL, err)
	}
	// Преобразуем []byte в string
	html := string(body)
	return html, resp, nil

}

// Получает слайс ссылок из html
func getLinksFromHtml(htmlBody string, baseURL string, opts Options) []string {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return []string{}
	}

	var links []string
	var linkCount int // счетчик найденных ссылок

	var findLinks func(*html.Node)
	findLinks = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			linkCount++
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					//fmt.Printf("Найден тег a с href: %s\n", attr.Val)
					normalized, err := NormalizeURL(attr.Val, baseURL)
					if err != nil {
						fmt.Printf("Ошибка нормализации %s: %v\n", attr.Val, err)
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
	//fmt.Printf("Всего найдено тегов a: %d, нормализовано ссылок: %d\n", linkCount, len(links))
	return links
}

// NormalizeURL нормализует относительные ссылки в абсолютные и проверяет валидность
func NormalizeURL(href, baseURL string) (string, error) {
	// Пропускаем якоря
	if strings.HasPrefix(href, "#") {
		return "", fmt.Errorf("skip anchor: %s", href)
	}

	// Пропускаем пустые ссылки
	if href == "" {
		return "", fmt.Errorf("empty href")
	}

	// Парсим базовый URL
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	// Парсим href как относительный/абсолютный URL
	parsed, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid href: %w", err)
	}

	// Разрешаем относительный URL относительно базового
	resolved := base.ResolveReference(parsed)

	// ИСПОЛЬЗУЕМ url.ParseRequestURI для строгой проверки
	_, err = url.ParseRequestURI(resolved.String())
	if err != nil {
		return "", fmt.Errorf("invalid resolved URL: %w", err)
	}

	// Проверяем, что схема - http или https
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme: %s (only http/https allowed)", resolved.Scheme)
	}

	// Проверяем, что есть хост
	if resolved.Host == "" {
		return "", fmt.Errorf("no host in URL: %s", resolved.String())
	}

	return resolved.String(), nil
}
*/

//*****************************************
/*
ВОТ ЭТО КОД все запрсы выполняет -
import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

func main() {
	// 8 ссылок для обхода
	urls := []string{
		"https://www.kamin-100.ru",
		"https://www.kamin-100.ru/krsk/foto/ovik-77.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-74.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-73.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-70.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-75.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-72.html",
		"https://www.kamin-100.ru/krsk/foto/ovik-71.html",
	}

	// Список различных User-Agent
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Edge/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (iPad; CPU OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
	}

	// Создаем клиент с таймаутом
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Обходим все ссылки
	for i, url := range urls {
		fmt.Printf("\n[%d/%d] Обработка: %s\n", i+1, len(urls), url)

		// Создаем новый запрос
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			fmt.Printf("Ошибка создания запроса: %v\n", err)
			continue
		}

		// Устанавливаем случайный User-Agent
		randomUA := userAgents[rand.Intn(len(userAgents))]
		req.Header.Set("User-Agent", randomUA)

		// Устанавливаем дополнительные заголовки

		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
		//req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Cache-Control", "no-cache")

		// Добавляем случайный заголовок X-Forwarded-For (опционально)
		randomIP := fmt.Sprintf("%d.%d.%d.%d", rand.Intn(255), rand.Intn(255), rand.Intn(255), rand.Intn(255))
		req.Header.Set("X-Forwarded-For", randomIP)

		fmt.Printf("User-Agent: %s\n", randomUA[:50]+"...")
		fmt.Printf("X-Forwarded-For: %s\n", randomIP)

		// Выполняем запрос
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Ошибка запроса: %v\n", err)
			// Продолжаем со следующим URL
			continue
		}

		// Читаем тело ответа (ограничиваем размер)
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // Максимум 1MB
		if err != nil {
			fmt.Printf("Ошибка чтения ответа: %v\n", err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		// Выводим информацию об ответе
		fmt.Printf("Статус: %s\n", resp.Status)
		fmt.Printf("Размер ответа: %d байт\n", len(body))
		fmt.Printf("Content-Type: %s\n", resp.Header.Get("Content-Type"))

		// Если нужно показать первые 200 символов ответа
		if len(body) > 0 {
			preview := string(body)
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Printf("Преview ответа: %s\n", preview)
		}

		// Генерируем случайную задержку от 3 до 10 секунд
		if i < len(urls)-1 { // Не делаем задержку после последнего запроса
			delay := rand.Intn(8) + 3 // 3-10 секунд
			fmt.Printf("Ожидание %d секунд перед следующим запросом...\n", delay)
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	fmt.Println("\n✅ Все запросы выполнены!")
}
*/
