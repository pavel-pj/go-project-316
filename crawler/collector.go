package crawler

import (
	"sort"
	"strings"
	"time"
)

// ResultCollector собирает результаты
type ResultCollector struct {
	pages       map[string]*Page
	brokenLinks []Link
	rootURL     string
	maxDepth    int32
	rootAdded   bool
	parser      *Parser
}

// NewResultCollector создает коллектор
func NewResultCollector(rootURL string, maxDepth int32) *ResultCollector {
	return &ResultCollector{
		pages:       make(map[string]*Page),
		brokenLinks: []Link{},
		rootURL:     rootURL,
		maxDepth:    maxDepth,
		parser:      NewParser(),
	}
}

// Process обрабатывает результат
func (rc *ResultCollector) Process(result Link, normalizedRoot string) {
	normalizedURL, _ := rc.parser.NormalizeURL(result.URL, rc.rootURL)
	isRoot := rc.isRootURL(normalizedURL, normalizedRoot)

	if rc.isErrorResult(result) {
		rc.handleErrorResult(result, normalizedURL, isRoot)
		return
	}

	if rc.isSuccessResult(result) {
		rc.handleSuccessResult(result, normalizedURL, isRoot)
	}
}

// ProcessBrokenLinks добавляет битые ссылки
func (rc *ResultCollector) ProcessBrokenLinks() {
	for _, brokenLink := range rc.brokenLinks {
		normalizedParent, _ := rc.parser.NormalizeURL(brokenLink.ParentURL, rc.rootURL)
		parentKey := strings.TrimSuffix(normalizedParent, "/")

		page := rc.findPage(parentKey, normalizedParent)
		if page != nil {
			rc.addBrokenLinkToPage(page, brokenLink)
		}
	}
}

// GetPages возвращает страницы
func (rc *ResultCollector) GetPages() []Page {
	var pages []Page
	for _, page := range rc.pages {
		rc.normalizePage(page)
		rc.sortPageAssets(page)
		pages = append(pages, *page)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].URL < pages[j].URL
	})

	return pages
}

func (rc *ResultCollector) isRootURL(normalizedURL, normalizedRoot string) bool {
	return normalizedURL == normalizedRoot ||
		strings.TrimSuffix(normalizedURL, "/") == strings.TrimSuffix(normalizedRoot, "/")
}

func (rc *ResultCollector) isErrorResult(result Link) bool {
	return result.Error != "" || (result.StatusCode != nil && *result.StatusCode >= 400)
}

func (rc *ResultCollector) isSuccessResult(result Link) bool {
	return result.StatusCode != nil && *result.StatusCode < 400
}

func (rc *ResultCollector) handleErrorResult(result Link, normalizedURL string, isRoot bool) {
	if !isRoot {
		rc.brokenLinks = append(rc.brokenLinks, result)
		return
	}

	if isRoot && !rc.rootAdded {
		rc.rootAdded = true
		rc.pages[normalizedURL] = &Page{
			URL:          rc.rootURL,
			Depth:        0,
			HttpStatus:   0,
			Status:       "error",
			SEO:          rc.parser.emptySEO(),
			Assets:       nil,
			BrokenLinks:  nil,
			Error:        rc.cleanErrorMessage(result.Error),
			DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
}

func (rc *ResultCollector) handleSuccessResult(result Link, normalizedURL string, isRoot bool) {
	status := strings.ToLower(result.ParentStatus)
	if status == "" {
		status = "ok"
	}

	seo := rc.parser.emptySEO()
	if result.SEO != nil {
		seo = *result.SEO
	}

	pageDepth := rc.calculateDepth(isRoot, result.Depth)

	mapKey := normalizedURL
	if mapKey == "" {
		mapKey = result.URL
	}

	if isRoot && rc.rootAdded {
		return
	}

	if isRoot {
		rc.rootAdded = true
	}

	assets := result.Assets
	if assets == nil {
		assets = []Asset{}
	}

	rc.pages[mapKey] = &Page{
		URL:          result.URL,
		Depth:        pageDepth,
		HttpStatus:   *result.StatusCode,
		Status:       status,
		SEO:          seo,
		Assets:       assets,
		BrokenLinks:  []BrokenLink{},
		DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func (rc *ResultCollector) calculateDepth(isRoot bool, resultDepth int) int {
	if isRoot {
		return 0
	}
	depth := int(rc.maxDepth) - resultDepth
	if depth < 1 {
		depth = 1
	}
	return depth
}

func (rc *ResultCollector) cleanErrorMessage(errMsg string) string {
	// Убираем префикс "cant handle request: "
	if strings.Contains(errMsg, "cant handle request:") {
		parts := strings.SplitN(errMsg, ", ", 2)
		if len(parts) > 1 {
			return parts[1]
		}
	}

	// Убираем "Get \"http://...\": " если есть
	if strings.Contains(errMsg, "Get \"") {
		parts := strings.SplitN(errMsg, ": ", 2)
		if len(parts) > 1 {
			return parts[1]
		}
	}

	return errMsg
}

func (rc *ResultCollector) findPage(parentKey, normalizedParent string) *Page {
	if page, exists := rc.pages[normalizedParent]; exists {
		return page
	}
	if page, exists := rc.pages[parentKey]; exists {
		return page
	}
	return nil
}

func (rc *ResultCollector) addBrokenLinkToPage(page *Page, brokenLink Link) {
	if page.BrokenLinks == nil {
		page.BrokenLinks = []BrokenLink{}
	}

	for _, existing := range page.BrokenLinks {
		if existing.URL == brokenLink.URL {
			return
		}
	}

	bl := BrokenLink{URL: brokenLink.URL, StatusCode: 404}
	if brokenLink.StatusCode != nil && *brokenLink.StatusCode >= 400 {
		bl.StatusCode = *brokenLink.StatusCode
	} else if brokenLink.Error != "" {
		bl.Error = brokenLink.Error
	}

	page.BrokenLinks = append(page.BrokenLinks, bl)
}

func (rc *ResultCollector) normalizePage(page *Page) {
	if page.Status != "error" {
		if page.Assets == nil {
			page.Assets = []Asset{}
		}
		if page.BrokenLinks == nil {
			page.BrokenLinks = []BrokenLink{}
		}
	}
}

func (rc *ResultCollector) sortPageAssets(page *Page) {
	if len(page.Assets) > 1 {
		sort.Slice(page.Assets, func(i, j int) bool {
			if page.Assets[i].Type != page.Assets[j].Type {
				return page.Assets[i].Type < page.Assets[j].Type
			}
			return page.Assets[i].URL < page.Assets[j].URL
		})
	}

	if len(page.BrokenLinks) > 1 {
		sort.Slice(page.BrokenLinks, func(i, j int) bool {
			return page.BrokenLinks[i].URL < page.BrokenLinks[j].URL
		})
	}
}
