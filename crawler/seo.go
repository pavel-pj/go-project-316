package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func getSeoFromHtml(htmlBody string) SEO {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		fmt.Printf("Ошибка парсинга HTML: %v\n", err)
		return SEO{
			HasTitle:       false,
			HasDescription: false,
			HasH1:          false,
		}
	}

	seo := SEO{
		HasTitle:       false,
		HasDescription: false,
		HasH1:          false,
	}

	var findElements func(*html.Node)
	findElements = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			titleText := strings.TrimSpace(extractText(n))
			if titleText != "" {
				seo.HasTitle = true
				seo.Title = titleText
			}
		}

		if n.Type == html.ElementNode && n.Data == "meta" {
			var isDescription bool
			var content string
			for _, attr := range n.Attr {
				if attr.Key == "name" && strings.ToLower(attr.Val) == "description" {
					isDescription = true
				}
				if attr.Key == "content" {
					content = attr.Val
				}
			}
			if isDescription && content != "" {
				seo.HasDescription = true
				seo.Description = content
			}
		}

		if n.Type == html.ElementNode && n.Data == "h1" && !seo.HasH1 {
			h1Text := strings.TrimSpace(extractText(n))
			if h1Text != "" {
				seo.HasH1 = true
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findElements(c)
		}
	}

	findElements(doc)
	return seo
}

func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		decoded := html.UnescapeString(n.Data)
		return strings.TrimSpace(decoded)
	}

	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text.WriteString(extractText(c))
	}
	return strings.TrimSpace(text.String())
}

// NormalizeURL нормализует URL
func NormalizeURL(href, baseURL string) (string, error) {
	if strings.HasPrefix(href, "#") {
		return "", errors.New("skip anchor")
	}

	if href == "" {
		return "", errors.New("empty href")
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	parsed, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("invalid href: %w", err)
	}

	resolved := base.ResolveReference(parsed)

	_, err = url.ParseRequestURI(resolved.String())
	if err != nil {
		return "", fmt.Errorf("invalid resolved URL: %w", err)
	}

	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme: %s (only http/https allowed)", resolved.Scheme)
	}

	if resolved.Host == "" {
		return "", fmt.Errorf("no host in URL: %s", resolved.String())
	}

	return resolved.String(), nil
}
