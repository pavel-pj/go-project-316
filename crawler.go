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

		delay := time.Duration(rng.Intn(4000)+1000) * time.Millisecond
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
		fmt.Printf("html:")
		fmt.Printf(html)
		links := getLinksFromHtml(html, job.URL, opts)
		fmt.Printf("Получил ссылок:")
		fmt.Println(links)
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

	// ✅ Устанавливаем User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// Дополнительные заголовки (чтобы выглядеть как реальный браузер)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	//req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

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
					fmt.Printf("Найден тег a с href: %s\n", attr.Val)
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
	fmt.Printf("Всего найдено тегов a: %d, нормализовано ссылок: %d\n", linkCount, len(links))
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
