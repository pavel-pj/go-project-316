package crawler

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

// SetupRateLimiter настраивает лимитер
func SetupRateLimiter(opts Options) {
	switch {
	case opts.RPS > 0:
		globalLimiter = rate.NewLimiter(rate.Limit(opts.RPS), 1)
	case opts.Delay > 0:
		rps := float64(time.Second) / float64(opts.Delay)
		globalLimiter = rate.NewLimiter(rate.Limit(rps), 1)
	default:
		globalLimiter = nil
	}
}

// WaitForRateLimit ожидает разрешения
func WaitForRateLimit(ctx context.Context) error {
	if globalLimiter == nil {
		return nil
	}
	if err := globalLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait failed: %w", err)
	}
	return nil
}

// IsNewLink проверяет, посещали ли ссылку
func IsNewLink(link string) bool {
	visitedMu.RLock()
	_, ok := visited[link]
	visitedMu.RUnlock()
	return !ok
}

// MarkVisited отмечает ссылку как посещенную
func MarkVisited(link string) {
	visitedMu.Lock()
	visited[link] = struct{}{}
	visitedMu.Unlock()
}
