start:
	go run cmd/hexlet-go-crawler/main.go	

lint:
	golangci-lint run ./...
	
build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler/main.go	

run:
	go run cmd/hexlet-go-crawler/main.go $(URL)


run-2:
	go run cmd/hexlet-go-crawler/main.go --depth=1 --rps=2 --retries=2 --indent=true http://example.com
test:
	go test -count=1 ./... -v
	