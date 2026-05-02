### Hexlet tests and linter status:
[![Actions Status](https://github.com/pavel-pj/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/pavel-pj/go-project-316/actions)
[![linter](https://github.com/pavel-pj/go-project-316/actions/workflows/linter.yml/badge.svg)](https://github.com/pavel-pj/go-project-316/actions/workflows/linter.yml)
[![Tests](https://github.com/pavel-pj/go-project-316/actions/workflows/test.yml/badge.svg)](https://github.com/pavel-pj/go-project-316/actions/workflows/test.yml)

# Site's Crawler (Golang)
 Программа обходит страницы сайта с указанной глубиной и выводит результат в о битых ссылках и ассетах, в т.ч на сторонние ресурсы.

#### Запуск :
```
make run URL=http://example.com
```

#### Или прямой вызов команды:
```
go run cmd/hexlet-go-crawler/main.go http://example.com
```

#### Пример команды с доп параметрами:
```bash
go run cmd/hexlet-go-crawler/main.go --depth=1 --rps=2 --retries=2 --indent=true http://example.com
```
#### возможные параметры:
-   `--depth value`       crawl depth (default: 10)
-   `--retries value`     number of retries for failed requests (default: 1)
-   `--delay value`       delay between requests (example: 200ms, 1s) (default: 0s)
-   `--timeout value`     per-request timeout (default: 15s)
-   `--rps value`         limit requests per second (overrides delay) (default: 0)
-   `--user-agent value`  custom user agent
-   `--workers value`     number of concurrent workers (default: 4)
-   `--help, -h `         show help 

## Ограничение скорости (--delay / --rps)
Данными параметрами можно указать программе частоту HTTP-запросов, чтобы не перегружать анализируемый сайт.

|     Флаг      |Описание                              
|---------------|---------------------------------------| 
| --delay=200ms | Фиксированная пауза между запросами                        |
| --rps=5       | Целевое количество запросов в секунду 

rps имеет приоритет , и если указаны оба параметра , то программа будет лимитировать запросы по rps

Если оба параметра не указаны, запросы отправляются максимально быстро.

Примеры

```bash
### 7 запросов в секунду 
make run URL=https://example.com RPS=7

### Фиксированная задержка 200 мирисекунд
make run URL=https://example.com DELAY=200ms
```


#### Результат:
```bash
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2024-06-01T12:34:56Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "error": "",
      "seo": {
        "has_title": true,
        "title": "Example title",
        "has_description": true,
        "description": "Example description",
        "has_h1": true
      },
      "broken_links": [
        {
          "url": "https://example.com/missing",
          "status_code": 404,
          "error": "Not Found"
        }
      ],
      "assets": [
        {
          "url": "https://example.com/static/logo.png",
          "type": "image",
          "status_code": 200,
          "size_bytes": 12345,
          "error": ""
        }
      ],
      "discovered_at": "2024-06-01T12:34:56Z"
    }
  ]
}
```