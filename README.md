### Hexlet tests and linter status:
[![Actions Status](https://github.com/pavel-pj/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/pavel-pj/go-project-316/actions)
[![linter](https://github.com/pavel-pj/go-project-316/actions/workflows/linter.yml/badge.svg)](https://github.com/pavel-pj/go-project-316/actions/workflows/linter.yml)



#### Разработка:
```
go run cmd/hexlet-go-crawler/main.go  --depth=4https://mail.ru	
```
#### или 
```
make run URL=https://mail.ru
go run cmd/hexlet-go-crawler/main.go  --depth=4	URL=http://localhost:8888


#### Собрать :
```bash
make build:
```

#### запуск краулера :
```
bin/hexlet-go-crawler https://mail.ru
```

#### справка :
```
bin/hexlet-go-crawler --help
```