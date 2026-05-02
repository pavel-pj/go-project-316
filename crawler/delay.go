package crawler

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// isRetriableError определяет, можно ли повторить запрос
func isRetriableError(err error, resp *http.Response) bool {
	if err != nil {
		errStr := err.Error()
		temporaryErrors := []string{
			"connection refused",
			"connection reset",
			"EOF",
			"timeout",
			"temporary failure",
			"no such host",
			"network is unreachable",
			"connection timed out",
			"i/o timeout",
			"broken pipe",
			"context deadline exceeded",
		}

		for _, tempErr := range temporaryErrors {
			if strings.Contains(strings.ToLower(errStr), tempErr) {
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
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return false
		}
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return true
		}
	}

	return false
}

func getRetryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	// nolint:gosec  // attempt всегда >=0 в нашем коде
	shift := uint(attempt)
	if shift > 30 {
		shift = 30
	}
	delay := time.Duration(1<<shift) * time.Second
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}

	jitter := time.Duration(rand.Int63n(int64(delay / 5)))
	if rand.Intn(2) == 0 {
		delay += jitter
	} else {
		delay -= jitter
	}

	if delay < 0 {
		delay = 0
	}

	return delay
}
