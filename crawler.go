package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Options struct {
	URL        string
	HTTPClient *http.Client
}

type Response struct {
	URL         string `json:"url"`
	Http_status string `json:"http_status"`
}

func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", opts.URL, nil)
	if err != nil {
		return []byte{}, fmt.Errorf("Cant prepare request to url:%s,  %w", opts.URL, err)
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return []byte{}, fmt.Errorf("Cant hadle request to url:%s,  %w", opts.URL, err)
	}

	defer resp.Body.Close()

	response := Response{
		URL:         opts.URL,
		Http_status: resp.Status,
	}

	return json.Marshal(response)

}
