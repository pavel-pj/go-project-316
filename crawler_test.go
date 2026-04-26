package crawler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	crawler "code"
)

func TestTimeoutViaContext(t *testing.T) {
	requestReceived := make(chan time.Time)

	// Сервер, который отвечает очень медленно (10 секунд)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived <- time.Now()
		t.Logf("[СЕРВЕР] Получил запрос в %s", time.Now().Format("15:04:05.000"))

		select {
		case <-time.After(10 * time.Second):
			t.Logf("[СЕРВЕР] Отправляю ответ через 10 секунд")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<html>Slow page</html>`))
		case <-r.Context().Done():
			t.Logf("[СЕРВЕР] Клиент отменил запрос через %v", time.Since(<-requestReceived))
			return
		}
	}))
	defer server.Close()

	// Клиент с большим таймаутом
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Контекст с таймаутом 3 секунды
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	opts := crawler.Options{
		URL:        server.URL,
		Depth:      1,
		HTTPClient: httpClient,
		Workers:    1,
	}

	start := time.Now()
	t.Logf("[КЛИЕНТ] Запускаю Analyze в %s", start.Format("15:04:05.000"))

	result, err := crawler.Analyze(ctx, opts)
	elapsed := time.Since(start)

	t.Logf("[КЛИЕНТ] Analyze вернулся через %v", elapsed)

	if err == nil {
		t.Errorf("Ошибки нет, хотя должен быть timeout! err=%v", err)
	} else {
		t.Logf("Ошибка: %v", err)
	}

	t.Logf("Результат: %v", result != nil)
}
