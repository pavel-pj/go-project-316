package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Тест на успешный ответ 200 OK
func TestAnalyze_Success(t *testing.T) {
	// Создаём тестовый сервер, который отвечает 200 OK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := server.Client()

	result, err := Analyze(context.Background(), Options{
		URL:        server.URL,
		HTTPClient: client,
	})

	// Проверяем, что нет ошибки
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Проверяем, что результат не пустой
	if len(result) == 0 {
		t.Fatal("Expected non-empty result, got empty")
	}

	// Парсим JSON ответ
	var response Response
	err = parseJSON(result, &response)
	if err != nil {
		t.Fatalf("Expected valid JSON, got error: %v", err)
	}

	// Проверяем статус
	if response.Http_status != "200 OK" {
		t.Errorf("Expected '200 OK', got '%s'", response.Http_status)
	}

	// Проверяем URL
	if response.URL != server.URL {
		t.Errorf("Expected URL '%s', got '%s'", server.URL, response.URL)
	}
}

// Тест на ошибку 400 Bad Request
func TestAnalyze_BadRequest(t *testing.T) {
	// Создаём тестовый сервер, который отвечает 400 Bad Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := server.Client()

	result, err := Analyze(context.Background(), Options{
		URL:        server.URL,
		HTTPClient: client,
	})

	// Проверяем, что нет ошибки (функция должна обработать 400 и вернуть JSON)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Проверяем, что результат не пустой
	if len(result) == 0 {
		t.Fatal("Expected non-empty result, got empty")
	}

	// Парсим JSON ответ
	var response Response
	err = parseJSON(result, &response)
	if err != nil {
		t.Fatalf("Expected valid JSON, got error: %v", err)
	}

	// Проверяем статус
	if response.Http_status != "400 Bad Request" {
		t.Errorf("Expected '400 Bad Request', got '%s'", response.Http_status)
	}

	// Проверяем URL
	if response.URL != server.URL {
		t.Errorf("Expected URL '%s', got '%s'", server.URL, response.URL)
	}
}

// Тест на разные статус-коды (табличный тест)
func TestAnalyze_VariousStatusCodes(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		expectedStatus string
	}{
		{
			name:           "200 OK",
			statusCode:     http.StatusOK,
			expectedStatus: "200 OK",
		},
		{
			name:           "201 Created",
			statusCode:     http.StatusCreated,
			expectedStatus: "201 Created",
		},
		{
			name:           "400 Bad Request",
			statusCode:     http.StatusBadRequest,
			expectedStatus: "400 Bad Request",
		},
		{
			name:           "401 Unauthorized",
			statusCode:     http.StatusUnauthorized,
			expectedStatus: "401 Unauthorized",
		},
		{
			name:           "403 Forbidden",
			statusCode:     http.StatusForbidden,
			expectedStatus: "403 Forbidden",
		},
		{
			name:           "404 Not Found",
			statusCode:     http.StatusNotFound,
			expectedStatus: "404 Not Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Создаём сервер с нужным статусом
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := server.Client()

			result, err := Analyze(context.Background(), Options{
				URL:        server.URL,
				HTTPClient: client,
			})

			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			var response Response
			err = parseJSON(result, &response)
			if err != nil {
				t.Fatalf("Expected valid JSON, got error: %v", err)
			}

			if response.Http_status != tt.expectedStatus {
				t.Errorf("Expected '%s', got '%s'", tt.expectedStatus, response.Http_status)
			}

			if response.URL != server.URL {
				t.Errorf("Expected URL '%s', got '%s'", server.URL, response.URL)
			}
		})
	}
}

// Вспомогательная функция для парсинга JSON
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
