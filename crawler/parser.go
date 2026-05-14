package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Parser парсит HTML/XML
type Parser struct{}

// NewParser создает парсер
func NewParser() *Parser {
	return &Parser{}
}

// ExtractLinks извлекает ссылки из HTML
func (p *Parser) ExtractLinks(htmlBody, baseURL string) []string {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return []string{}
	}

	var links []string
	var findLinks func(*html.Node)
	findLinks = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					if normalized, err := p.NormalizeURL(attr.Val, baseURL); err == nil {
						links = append(links, normalized)
					}
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

// ExtractSEO извлекает SEO из HTML
func (p *Parser) ExtractSEO(htmlBody string) SEO {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return p.emptySEO()
	}

	seo := p.emptySEO()
	var findElements func(*html.Node)
	findElements = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if text := strings.TrimSpace(p.extractText(n)); text != "" {
					seo.HasTitle = true
					seo.Title = text
				}
			case "meta":
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
			case "h1":
				if !seo.HasH1 {
					text := strings.TrimSpace(p.extractText(n))
					if text != "" {
						seo.HasH1 = true
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findElements(c)
		}
	}

	findElements(doc)
	return seo
}

// ExtractSEOFromXML извлекает SEO из XML
func (p *Parser) ExtractSEOFromXML(xmlBody string) SEO {
	seo := p.emptySEO()
	titleStart := strings.Index(xmlBody, "<title")
	if titleStart != -1 {
		tagClose := strings.Index(xmlBody[titleStart:], ">")
		if tagClose != -1 {
			contentStart := titleStart + tagClose + 1
			titleEnd := strings.Index(xmlBody[contentStart:], "</title>")
			if titleEnd != -1 {
				title := strings.TrimSpace(xmlBody[contentStart : contentStart+titleEnd])
				if title != "" {
					seo.HasTitle = true
					seo.Title = title
				}
			}
		}
	}
	return seo
}

// NormalizeURL нормализует URL
func (p *Parser) NormalizeURL(href, baseURL string) (string, error) {
	if strings.HasPrefix(href, "#") || href == "" {
		return "", errors.New("skip invalid url")
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

	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme: %s", resolved.Scheme)
	}

	if resolved.Host == "" {
		return "", fmt.Errorf("no host in URL: %s", resolved.String())
	}

	return resolved.String(), nil
}

// IsSameDomain проверяет домен
func (p *Parser) IsSameDomain(link, domain string) bool {
	domainURL, err := url.Parse(domain)
	if err != nil {
		return false
	}
	linkURL, err := url.Parse(link)
	if err != nil {
		return false
	}
	return strings.EqualFold(domainURL.Host, linkURL.Host)
}

func (p *Parser) emptySEO() SEO {
	return SEO{
		HasTitle:       false,
		Title:          "",
		HasDescription: false,
		Description:    "",
		HasH1:          false,
	}
}

func (p *Parser) extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return strings.TrimSpace(html.UnescapeString(n.Data))
	}
	var text strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text.WriteString(p.extractText(c))
	}
	return strings.TrimSpace(text.String())
}

// GetAssetType определяет тип ассета
func GetAssetType(urlStr, contentType string) string {
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
