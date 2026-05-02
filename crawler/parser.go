package crawler

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

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

// getRandomUserAgent возвращает случайный User-Agent
func getRandomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return userAgents[rng.Intn(len(userAgents))]
}

func doRequest(ctx context.Context, link string, opts Options, rng *rand.Rand, workerID int) (string, *http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant prepare request to url:%s, %w", link, err)
	}

	randomUA := getRandomUserAgent()
	req.Header.Set("User-Agent", randomUA)

	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")

	randomIP := getRandomIP()
	req.Header.Set("X-Forwarded-For", randomIP)

	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")

	if rng.Float64() < 0.7 {
		parts := strings.Split(link, "/")
		if len(parts) > 2 {
			referer := fmt.Sprintf("https://%s/", parts[2])
			req.Header.Set("Referer", referer)
		}
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant handle request to url:%s, %w", link, err)
	}

	parentStatus := ""
	parts := strings.Split(resp.Status, " ")
	if len(parts) == 2 {
		parentStatus = parts[1]
	}

	return "", resp, parentStatus, nil
}

func fetchWithRetry(ctx context.Context, link string, opts Options, rng *rand.Rand, workerID int, isAsset bool) (string, *http.Response, string, error) {
	var lastResp *http.Response
	var lastErr error
	var lastBody string
	var lastStatus string

	maxRetries := opts.Retries
	if maxRetries < 0 {
		maxRetries = 0
	}

	if isAsset && maxRetries > 2 {
		maxRetries = 2
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", lastResp, "", fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		if attempt > 0 {
			delay := getRetryDelay(attempt-1, lastResp)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", lastResp, "", fmt.Errorf("context cancelled: %w", ctx.Err())
			}
		}

		body, resp, status, err := doRequest(ctx, link, opts, rng, workerID)

		// Если это последняя попытка или ошибка не retriable, возвращаем resp
		// Но перед возвратом нужно закрыть body в других случаях
		if err == nil && resp != nil && resp.StatusCode < 400 {
			// Успешный ответ - возвращаем, тело будет закрыто вызывающей стороной
			return body, resp, status, nil
		}

		// Сохраняем для возможного следующего retry
		if resp != nil {
			lastResp = resp
		}
		lastErr = err
		lastBody = body
		lastStatus = status

		// Если это последняя попытка
		if attempt == maxRetries {
			// Возвращаем resp - вызывающая сторона должна закрыть тело
			return body, resp, status, err
		}

		// Если ошибка не retriable, возвращаем результат без retry
		if !isRetriableError(err, resp) {
			// Закрываем тело, если оно есть, так как мы не возвращаем resp наружу
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return body, resp, status, err
		}

		// Закрываем тело перед следующей попыткой, если оно есть
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	return lastBody, lastResp, lastStatus, lastErr
}

// getRandomIP возвращает случайный IP адрес
//
//nolint:gosec
func getRandomIP() string {

	return fmt.Sprintf("%d.%d.%d.%d",
		//nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255), //nolint:gosec
		rand.Intn(255)) //nolint:gosec
}

func getAssetType(urlStr string, contentType string) string {
	if contentType != "" {
		contentType = strings.ToLower(contentType)
		switch {
		case strings.Contains(contentType, "image"):
			return "image"
		case strings.Contains(contentType, "javascript"), strings.Contains(contentType, "ecmascript"):
			return "script"
		case strings.Contains(contentType, "css"):
			return "style"
		}
	}

	urlLower := strings.ToLower(urlStr)
	switch {
	case strings.Contains(urlLower, ".jpg"), strings.Contains(urlLower, ".jpeg"),
		strings.Contains(urlLower, ".png"), strings.Contains(urlLower, ".gif"),
		strings.Contains(urlLower, ".svg"), strings.Contains(urlLower, ".webp"),
		strings.Contains(urlLower, ".ico"):
		return "image"
	case strings.Contains(urlLower, ".js"):
		return "script"
	case strings.Contains(urlLower, ".css"):
		return "style"
	default:
		return "other"
	}
}

func fetchAsset(ctx context.Context, assetURL string, opts Options, rng *rand.Rand, workerID int) Asset {
	// Проверяем кэш
	assetsCacheMu.RLock()
	if cached, exists := assetsCache[assetURL]; exists {
		assetsCacheMu.RUnlock()

		return cached
	}
	assetsCacheMu.RUnlock()

	// Ждём rate limiter
	if err := waitForRateLimit(ctx); err != nil {
		return Asset{
			URL:        assetURL,
			Type:       getAssetType(assetURL, ""),
			StatusCode: 0,
			SizeBytes:  0,
			Error:      err.Error(),
		}
	}

	// Выполняем запрос с retry
	_, resp, _, err := fetchWithRetry(ctx, assetURL, opts, rng, workerID, true)

	if resp != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				_ = err
			}
		}()
	}

	asset := Asset{
		URL:        assetURL,
		Type:       getAssetType(assetURL, ""),
		StatusCode: 0,
		SizeBytes:  0,
	}

	if err != nil {
		asset.Error = err.Error()
	} else if resp != nil {
		asset.StatusCode = resp.StatusCode

		if resp.StatusCode >= 400 {
			asset.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status)
		} else {
			contentType := resp.Header.Get("Content-Type")
			asset.Type = getAssetType(assetURL, contentType)

			contentLength := resp.Header.Get("Content-Length")
			if contentLength != "" {
				if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
					asset.SizeBytes = size
				} else {
					body, err := io.ReadAll(resp.Body)
					if err == nil {
						asset.SizeBytes = int64(len(body))
					} else {
						asset.Error = fmt.Sprintf("failed to read body: %v", err)
					}
					resp.Body = io.NopCloser(strings.NewReader(string(body)))
				}
			} else {
				body, err := io.ReadAll(resp.Body)
				if err == nil {
					asset.SizeBytes = int64(len(body))
				} else {
					asset.Error = fmt.Sprintf("failed to read body: %v", err)
				}
				resp.Body = io.NopCloser(strings.NewReader(string(body)))
			}
		}
	}

	assetsCacheMu.Lock()
	assetsCache[assetURL] = asset
	assetsCacheMu.Unlock()

	return asset
}

func extractAssetsFromHtml(htmlBody string, baseURL string, opts Options, ctx context.Context, rng *rand.Rand, workerID int) []Asset {
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
			case "img":
				for _, attr := range n.Attr {
					if attr.Key == "src" {
						if assetURL, err := NormalizeURL(attr.Val, baseURL); err == nil {
							if !assetURLs[assetURL] {
								assetURLs[assetURL] = true
								asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
								assets = append(assets, asset)
							}
						}
					}
				}
			case "script":
				for _, attr := range n.Attr {
					if attr.Key == "src" {
						if assetURL, err := NormalizeURL(attr.Val, baseURL); err == nil {
							if !assetURLs[assetURL] {
								assetURLs[assetURL] = true
								asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
								assets = append(assets, asset)
							}
						}
					}
				}
			case "link":
				for _, attr := range n.Attr {
					if attr.Key == "rel" && strings.Contains(strings.ToLower(attr.Val), "stylesheet") {
						for _, attr2 := range n.Attr {
							if attr2.Key == "href" {
								if assetURL, err := NormalizeURL(attr2.Val, baseURL); err == nil {
									if !assetURLs[assetURL] {
										assetURLs[assetURL] = true
										asset := fetchAsset(ctx, assetURL, opts, rng, workerID)
										assets = append(assets, asset)
									}
								}
							}
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findAssets(c)
		}
	}

	findAssets(doc)
	return assets
}
func isNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
}

func getLinksFromHtml(htmlBody string, baseURL string, opts Options) []string {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		// fmt.Printf("Ошибка парсинга HTML: %v\n", err)
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
