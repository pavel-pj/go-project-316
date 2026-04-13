package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

	// Читаем тело ответа
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return []byte{}, fmt.Errorf("Cant read response body from url:%s, %w", opts.URL, err)
	}

	links := getLinksFromHtml(bodyBytes)
	fmt.Println(links)

	response := Response{
		URL:         opts.URL,
		Http_status: resp.Status,
	}

	return json.Marshal(response)

}

func getLinksFromHtml(htmlBytes []byte) []string {
	// Преобразуем []byte в string
	htmlString := string(htmlBytes)

	// Парсим HTML
	doc, err := html.Parse(strings.NewReader(htmlString))
	if err != nil {
		return []string{} // возвращаем пустой срез при ошибке
	}

	var rawLinks []string

	var findLinks func(*html.Node)
	findLinks = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					rawLinks = append(rawLinks, attr.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findLinks(c)
		}
	}

	findLinks(doc)

	//удаляем дубликаты
	links := uniqueStrings(rawLinks)

	return links
}

func uniqueStrings(input []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, val := range input {
		if !seen[val] {
			seen[val] = true
			result = append(result, val)
		}
	}
	return result
}

func checkPage()
