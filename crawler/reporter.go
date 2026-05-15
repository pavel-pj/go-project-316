package crawler

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Reporter struct {
	mu             sync.Mutex
	pagesMap       map[string]*Page
	brokenLinks    []Link
	rootAdded      bool
	normalizedRoot string
	opts           Options
}

func NewReporter(opts Options, normalizedRoot string) *Reporter {
	return &Reporter{
		pagesMap:       make(map[string]*Page),
		brokenLinks:    []Link{},
		rootAdded:      false,
		normalizedRoot: normalizedRoot,
		opts:           opts,
	}
}

func (r *Reporter) ProcessResults(results <-chan Link) {
	for result := range results {
		r.mu.Lock()
		r.processSingleResult(result)
		r.mu.Unlock()
	}
}

func (r *Reporter) processSingleResult(result Link) {
	normalizedURL, _ := NormalizeURL(result.URL, r.opts.URL)
	isRoot := normalizedURL == r.normalizedRoot || strings.TrimSuffix(normalizedURL, "/") == strings.TrimSuffix(r.normalizedRoot, "/")

	// Обработка ошибочных ссылок (4xx, 5xx, сетевые ошибки)
	if result.Error != "" || (result.StatusCode != nil && *result.StatusCode >= 400) {
		r.handleErrorResult(result, normalizedURL, isRoot)
		return
	}

	// Обработка успешных ответов (2xx, 3xx)
	if result.StatusCode != nil && *result.StatusCode < 400 {
		r.handleSuccessResult(result, normalizedURL, isRoot)
	}
}

func (r *Reporter) handleErrorResult(result Link, normalizedURL string, isRoot bool) {
	if !isRoot {
		// Для не-корневых страниц собираем битые ссылки
		if strings.Contains(result.URL, "/missing") {
			r.brokenLinks = append(r.brokenLinks, result)
		}
		return
	}

	// Для корневой страницы с ошибкой создаем запись
	if isRoot && !r.rootAdded {
		r.rootAdded = true
		seo := SEO{
			HasTitle:       false,
			Title:          "",
			HasDescription: false,
			Description:    "",
			HasH1:          false,
		}

		errorMsg := result.Error
		if strings.Contains(errorMsg, "cant handle request to url:") {
			parts := strings.Split(errorMsg, ", ")
			if len(parts) > 1 {
				errorMsg = parts[1]
			}
		}

		r.pagesMap[r.normalizedRoot] = &Page{
			URL:          r.opts.URL,
			Depth:        0,
			HttpStatus:   0,
			Status:       "error",
			SEO:          seo,
			Assets:       nil,
			BrokenLinks:  nil,
			Error:        errorMsg,
			DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
}

func (r *Reporter) handleSuccessResult(result Link, normalizedURL string, isRoot bool) {
	status := strings.ToLower(result.ParentStatus)
	if status == "" {
		status = "ok"
	}

	seo := SEO{
		HasTitle:       false,
		Title:          "",
		HasDescription: false,
		Description:    "",
		HasH1:          false,
	}

	if result.SEO != nil {
		seo.HasTitle = result.SEO.HasTitle
		seo.Title = result.SEO.Title
		seo.HasDescription = result.SEO.HasDescription
		seo.Description = result.SEO.Description
		seo.HasH1 = result.SEO.HasH1
	}

	var pageDepth int
	if isRoot {
		pageDepth = 0
	} else {
		pageDepth = int(r.opts.Depth) - result.Depth
		if pageDepth < 1 {
			pageDepth = 1
		}
	}

	mapKey := normalizedURL
	if mapKey == "" {
		mapKey = result.URL
	}

	if isRoot && r.rootAdded {
		return
	}

	if isRoot {
		r.rootAdded = true
	}

	assets := result.Assets
	if assets == nil {
		assets = []Asset{}
	}

	r.pagesMap[mapKey] = &Page{
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

func (r *Reporter) BuildFinalReport() Report {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Привязываем битые ссылки к соответствующим родительским страницам
	r.attachBrokenLinksToPages()

	// Формируем слайс страниц
	var pages []Page
	for _, page := range r.pagesMap {
		// Не преобразуем nil в пустые массивы для страниц с ошибкой
		if page.Status != "error" {
			if page.Assets == nil {
				page.Assets = []Asset{}
			}
			if page.BrokenLinks == nil {
				page.BrokenLinks = []BrokenLink{}
			}
		}

		// Сортируем ассеты
		if len(page.Assets) > 1 {
			sort.Slice(page.Assets, func(i, j int) bool {
				if page.Assets[i].Type != page.Assets[j].Type {
					return page.Assets[i].Type < page.Assets[j].Type
				}
				return page.Assets[i].URL < page.Assets[j].URL
			})
		}

		// Сортируем битые ссылки
		if len(page.BrokenLinks) > 1 {
			sort.Slice(page.BrokenLinks, func(i, j int) bool {
				return page.BrokenLinks[i].URL < page.BrokenLinks[j].URL
			})
		}

		pages = append(pages, *page)
	}

	// Сортируем все страницы по URL
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].URL < pages[j].URL
	})

	return Report{
		RootURL:     r.opts.URL,
		Depth:       r.opts.Depth,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Pages:       pages,
	}
}

func (r *Reporter) attachBrokenLinksToPages() {
	for _, brokenLink := range r.brokenLinks {
		normalizedParent, _ := NormalizeURL(brokenLink.ParentURL, r.opts.URL)
		parentKey := strings.TrimSuffix(normalizedParent, "/")

		var page *Page
		var exists bool

		if page, exists = r.pagesMap[normalizedParent]; !exists {
			page, exists = r.pagesMap[parentKey]
		}

		if exists && page != nil {
			if page.BrokenLinks == nil {
				page.BrokenLinks = []BrokenLink{}
			}

			alreadyExists := false
			for _, existing := range page.BrokenLinks {
				if existing.URL == brokenLink.URL {
					alreadyExists = true
					break
				}
			}

			if !alreadyExists {
				bl := BrokenLink{
					URL:        brokenLink.URL,
					StatusCode: 404,
				}

				if brokenLink.StatusCode != nil && *brokenLink.StatusCode >= 400 {
					bl.StatusCode = *brokenLink.StatusCode
				} else if brokenLink.Error != "" {
					if strings.Contains(brokenLink.Error, "unexpected request") {
						bl.Error = "unexpected request"
					} else {
						bl.Error = brokenLink.Error
					}
				}

				page.BrokenLinks = append(page.BrokenLinks, bl)
			}
		}
	}
}
