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
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
}

// HTTPClient HTTP клиент с ретраями
type HTTPClient struct {
	client  *http.Client
	retries int
	rng     *rand.Rand
	parser  *Parser
}

// NewHTTPClient создает клиент
func NewHTTPClient(opts Options, workerID int) *HTTPClient {
	return &HTTPClient{
		client:  opts.HTTPClient,
		retries: opts.Retries,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID))),
		parser:  NewParser(),
	}
}

// Fetch выполняет запрос
func (c *HTTPClient) Fetch(ctx context.Context, url string, isAsset bool) (*InternalResponse, error) {
	maxRetries := c.retries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if isAsset && maxRetries > MaxRetriesForAsset {
		maxRetries = MaxRetriesForAsset
	}

	var lastResp *http.Response
	var lastErr error
	var lastBody string
	var lastStatus string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if attempt > 0 {
			delay := c.getRetryDelay(attempt-1, lastResp)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		body, resp, status, err := c.doRequest(ctx, url)

		if err == nil && resp != nil && resp.StatusCode < 400 {
			return &InternalResponse{
				Body:       body,
				StatusCode: resp.StatusCode,
				Status:     status,
				Header:     resp.Header,
			}, nil
		}

		if resp != nil {
			lastResp = resp
		}
		lastErr = err
		lastBody = body
		lastStatus = status

		if attempt == maxRetries {
			if resp != nil {
				return &InternalResponse{
					Body:       lastBody,
					StatusCode: resp.StatusCode,
					Status:     lastStatus,
					Header:     resp.Header,
				}, lastErr
			}
			return nil, lastErr
		}

		if !c.isRetriableError(err, resp) {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if resp != nil {
				return &InternalResponse{
					Body:       lastBody,
					StatusCode: resp.StatusCode,
					Status:     lastStatus,
					Header:     resp.Header,
				}, lastErr
			}
			return nil, lastErr
		}

		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doRequest - исправленный формат ошибки
func (c *HTTPClient) doRequest(ctx context.Context, url string) (string, *http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", nil, "", fmt.Errorf("cant prepare request: %s, %w", url, err)
	}

	c.setHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		// ТОЧНО ТАКОЙ ЖЕ ФОРМАТ, КАК В СТАРОМ КОДЕ
		return "", nil, "", fmt.Errorf("cant handle request to url:%s, %w", url, err)
	}

	status := ""
	parts := strings.Split(resp.Status, " ")
	if len(parts) == 2 {
		status = parts[1]
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp, status, err
	}

	return string(body), resp, status, nil
}

func (c *HTTPClient) setHeaders(req *http.Request) {
	randomUA := userAgents[c.rng.Intn(len(userAgents))]
	req.Header.Set("User-Agent", randomUA)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("X-Forwarded-For", c.getRandomIP())

	if c.rng.Float64() < 0.7 {
		parts := strings.Split(req.URL.String(), "/")
		if len(parts) > 2 {
			referer := fmt.Sprintf("https://%s/", parts[2])
			req.Header.Set("Referer", referer)
		}
	}
}

func (c *HTTPClient) getRandomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		c.rng.Intn(255),
		c.rng.Intn(255),
		c.rng.Intn(255),
		c.rng.Intn(255))
}

func (c *HTTPClient) isRetriableError(err error, resp *http.Response) bool {
	if err != nil {
		errStr := strings.ToLower(err.Error())
		temporaryErrors := []string{
			"connection refused", "connection reset", "EOF",
			"timeout", "temporary failure", "no such host",
			"network is unreachable", "connection timed out",
			"i/o timeout", "broken pipe", "context deadline exceeded",
		}
		for _, tempErr := range temporaryErrors {
			if strings.Contains(errStr, tempErr) {
				return true
			}
		}
		return false
	}

	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusRequestTimeout,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return resp.StatusCode >= 500 && resp.StatusCode <= 599
	}
}

func (c *HTTPClient) getRetryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	shift := uint(attempt)
	if shift > 30 {
		shift = 30
	}
	delay := time.Duration(1<<shift) * time.Second
	if delay > MaxRetryDelay {
		delay = MaxRetryDelay
	}

	jitter := time.Duration(c.rng.Int63n(int64(delay / 5)))
	if c.rng.Intn(2) == 0 {
		delay += jitter
	} else {
		delay -= jitter
	}

	if delay < 0 {
		delay = 0
	}

	return delay
}
