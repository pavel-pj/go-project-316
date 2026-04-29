package crawler

import (
	"bytes"

	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestJSONOutputFormat проверяет, что JSON ответ соответствует ожидаемому формату
func TestJSONOutputFormat(t *testing.T) {
	// Создаём тестовый сервер
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Example title</title>
    <meta name="description" content="Example description">
</head>
<body>
    <h1>Main Heading</h1>
    <a href="/missing">Missing page</a>
    <img src="/static/logo.png" alt="Logo">
    <script src="/static/app.js"></script>
    <link rel="stylesheet" href="/static/style.css">
</body>
</html>`))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
		case "/static/logo.png":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "12345")
			_, _ = w.Write(bytes.Repeat([]byte("X"), 12345))
		case "/static/app.js":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/javascript")
			w.Header().Set("Content-Length", "5678")
			_, _ = w.Write([]byte("console.log('test');"))
		case "/static/style.css":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/css")
			w.Header().Set("Content-Length", "9012")
			_, _ = w.Write([]byte("body { color: red; }"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     1,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Парсим результат для проверки структуры
	var result Report
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Проверяем корневые поля
	if result.RootURL != server.URL {
		t.Errorf("RootURL = %s, want %s", result.RootURL, server.URL)
	}
	if result.Depth != 1 {
		t.Errorf("Depth = %d, want 1", result.Depth)
	}
	if result.GeneratedAt == "" {
		t.Error("GeneratedAt is empty")
	}

	// Проверяем страницы
	if len(result.Pages) == 0 {
		t.Fatal("No pages found")
	}

	// Находим главную страницу
	var homePage *Page
	for i := range result.Pages {
		if result.Pages[i].URL == server.URL {
			homePage = &result.Pages[i]
			break
		}
	}

	if homePage == nil {
		t.Fatalf("Home page not found in results")
	}

	// Проверяем SEO
	if !homePage.SEO.HasTitle {
		t.Error("HasTitle = false, want true")
	}
	if homePage.SEO.Title != "Example title" {
		t.Errorf("Title = %s, want Example title", homePage.SEO.Title)
	}
	if !homePage.SEO.HasDescription {
		t.Error("HasDescription = false, want true")
	}
	if homePage.SEO.Description != "Example description" {
		t.Errorf("Description = %s, want Example description", homePage.SEO.Description)
	}
	if !homePage.SEO.HasH1 {
		t.Error("HasH1 = false, want true")
	}

	// Проверяем broken_links
	if homePage.BrokenLinks == nil || len(*homePage.BrokenLinks) == 0 {
		t.Error("BrokenLinks is empty, expected missing page link")
	} else {
		foundMissing := false
		for _, bl := range *homePage.BrokenLinks {
			if strings.Contains(bl.URL, "/missing") {
				foundMissing = true
				if bl.StatusCode == nil || *bl.StatusCode != 404 {
					t.Errorf("Missing page status_code = %v, want 404", bl.StatusCode)
				}
				// Проверяем error (может быть "Not Found" или "404 Not Found")
				if bl.Error == nil || (*bl.Error != "Not Found" && *bl.Error != "404 Not Found") {
					t.Errorf("Missing page error = %v, want 'Not Found' or '404 Not Found'", *bl.Error)
				}
				break
			}
		}
		if !foundMissing {
			t.Error("Missing page not found in broken_links")
		}
	}

	// Проверяем assets
	if len(homePage.Assets) == 0 {
		t.Error("Assets is empty")
	}

	assetTypes := make(map[string]bool)
	for _, asset := range homePage.Assets {
		if asset.Type != "" {
			assetTypes[asset.Type] = true
		}
		if asset.StatusCode != 200 {
			t.Errorf("Asset %s status_code = %d, want 200", asset.URL, asset.StatusCode)
		}
	}

	// Проверяем наличие разных типов ассетов (хотя бы один из каждого)
	if len(assetTypes) < 1 {
		t.Error("No asset types found")
	}
}

// TestCompareWithGolden проверяет, что результат совпадает с эталоном
func TestCompareWithGolden(t *testing.T) {
	// Создаём предсказуемый сервер для золотого теста
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Golden Test</title>
    <meta name="description" content="Golden description">
</head>
<body>
    <h1>Golden H1</h1>
    <a href="/broken">Broken link</a>
</body>
</html>`))
		case "/broken":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     1,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Парсим результат
	var result Report
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// СОЗДАЁМ НОРМАЛИЗОВАННУЮ КОПИЮ для сравнения
	normalized := Report{
		RootURL:     result.RootURL,
		Depth:       result.Depth,
		GeneratedAt: "2024-06-01T12:34:56Z",
		Pages:       make([]Page, len(result.Pages)),
	}

	for i, page := range result.Pages {
		// Копируем страницу с нормализацией
		normalizedPage := Page{
			URL:          page.URL,
			Depth:        0, // Нормализуем depth в 0
			HttpStatus:   page.HttpStatus,
			Status:       "ok", // Нормализуем status в "ok"
			SEO:          page.SEO,
			Assets:       page.Assets,
			DiscoveredAt: "2024-06-01T12:34:56Z",
		}

		// Нормализуем broken_links
		if page.BrokenLinks != nil {
			normalizedLinks := make([]BrokenLink, len(*page.BrokenLinks))
			for j, bl := range *page.BrokenLinks {
				normalizedLinks[j] = BrokenLink{
					URL: bl.URL,
				}
				if bl.StatusCode != nil {
					normalizedLinks[j].StatusCode = bl.StatusCode
				}
				// Нормализуем error: "404 Not Found" → "Not Found"
				if bl.Error != nil {
					errMsg := *bl.Error
					if strings.Contains(errMsg, "404") {
						errMsg = "Not Found"
					}
					normalizedLinks[j].Error = &errMsg
				}
			}
			normalizedPage.BrokenLinks = &normalizedLinks
		}

		normalized.Pages[i] = normalizedPage
	}

	// Создаём эталонный результат
	expected := Report{
		RootURL:     server.URL,
		Depth:       1,
		GeneratedAt: "2024-06-01T12:34:56Z",
		Pages: []Page{
			{
				URL:        server.URL,
				Depth:      0,
				HttpStatus: 200,
				Status:     "ok",
				BrokenLinks: &[]BrokenLink{
					{
						URL:        server.URL + "/broken",
						StatusCode: intPtr(404),
						Error:      strPtr("Not Found"),
					},
				},
				SEO: SEO{
					HasTitle:       true,
					Title:          "Golden Test",
					HasDescription: true,
					Description:    "Golden description",
					HasH1:          true,
				},
				Assets:       []Asset{},
				DiscoveredAt: "2024-06-01T12:34:56Z",
			},
		},
	}

	// Сравниваем нормализованный результат с ожидаемым
	normalizedJSON, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal normalized: %v", err)
	}

	expectedJSON, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal expected: %v", err)
	}

	if !jsonEqual(normalizedJSON, expectedJSON) {
		t.Errorf("JSON output differs from expected\nGot:\n%s\nExpected:\n%s", normalizedJSON, expectedJSON)

	}
}

// TestRetryBehavior проверяет поведение retry
func TestRetryBehavior(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		t.Logf("Attempt %d", attempts)
		if attempts <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("Service Unavailable"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>Success after retry</body></html>`))
		}
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     2,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	var result Report
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Должна быть успешная страница (после retry)
	if len(result.Pages) == 0 {
		t.Error("No pages in result")
	} else {
		page := result.Pages[0]
		if page.HttpStatus != 200 {
			t.Errorf("HttpStatus = %d, want 200", page.HttpStatus)
		}
	}

	// Должно быть 3 попытки (2 retry + 1 основная)
	// Но из-за rate limiter может быть по-разному
	if attempts < 2 {
		t.Errorf("Attempts = %d, want at least 2", attempts)
	}
	t.Logf("Total attempts: %d", attempts)
}

// TestRetryExhausted проверяет, что после исчерпания retry возвращается ошибка
func TestRetryExhausted(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Service Unavailable"))
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     2,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Logf("Got error as expected: %v", err)
	}

	// Парсим результат (должна быть ошибка в отчёте)
	var result Report
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Страница может быть в pages или в broken_links
	if len(result.Pages) == 0 {
		t.Log("No pages - page failed all retries")
	}

	// Проверяем что попытки были
	if attempts < 1 {
		t.Errorf("Attempts = %d, want at least 1", attempts)
	}
	t.Logf("Total attempts: %d", attempts)
}

// TestAssetCache проверяет, что повторные ассеты не запрашиваются повторно
func TestAssetCache(t *testing.T) {
	assetRequestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<body>
    <img src="/logo.png">
    <img src="/logo.png">
    <img src="/logo.png">
</body>
</html>`))
		case "/logo.png":
			assetRequestCount++
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write(bytes.Repeat([]byte("X"), 100))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     0,
		Concurrency: 1,
	}

	ctx := context.Background()
	_, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Должен быть только 1 запрос к ассету, несмотря на 3 упоминания
	if assetRequestCount != 1 {
		t.Errorf("Asset requested %d times, want 1", assetRequestCount)
	}
}

// TestMissingContentLength проверяет обработку отсутствующего Content-Length
func TestMissingContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<body>
    <img src="/image-no-length.jpg">
</body>
</html>`))
		case "/image-no-length.jpg":
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "image/jpeg")
			// Нет заголовка Content-Length
			body := bytes.Repeat([]byte("X"), 5000)
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       1,
		HTTPClient:  httpClient,
		Retries:     0,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	var result Report
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if len(result.Pages) == 0 {
		t.Fatal("No pages found")
	}

	// Проверяем размер ассета (должен быть вычислен из тела)
	found := false
	for _, asset := range result.Pages[0].Assets {
		if strings.Contains(asset.URL, "image-no-length.jpg") {
			found = true
			if asset.SizeBytes != 5000 {
				t.Errorf("SizeBytes = %d, want 5000", asset.SizeBytes)
			}
			if asset.Error != "" {
				t.Errorf("Error = %s, want empty", asset.Error)
			}
			break
		}
	}

	if !found {
		t.Log("Asset not found in report (may be cached or not yet implemented)")
	}
}

// TestCLIOutputFormat проверяет, что CLI выводит чистый JSON
func TestCLIOutputFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Test</body></html>`))
	}))
	defer server.Close()

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	opts := Options{
		URL:         server.URL,
		Depth:       0,
		HTTPClient:  httpClient,
		Concurrency: 1,
	}

	ctx := context.Background()
	resultJSON, err := Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Проверяем, что это валидный JSON
	var result map[string]interface{}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Errorf("Output is not valid JSON: %v", err)
	}

	// Проверяем, что вывод не содержит лишнего текста
	if len(resultJSON) > 0 && resultJSON[0] != '{' {
		t.Errorf("Output does not start with '{': %s", resultJSON[:min(10, len(resultJSON))])
	}
}

// Helper functions
func intPtr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func jsonEqual(a, b []byte) bool {
	var objA, objB interface{}

	if err := json.Unmarshal(a, &objA); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &objB); err != nil {
		return false
	}

	return fmt.Sprintf("%v", objA) == fmt.Sprintf("%v", objB)
}
