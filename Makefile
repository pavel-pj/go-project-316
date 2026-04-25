start:
	go run cmd/hexlet-go-crawler/main.go	

lint:
	golangci-lint run ./...
	
build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler/main.go	
	
run:
	go run cmd/hexlet-go-crawler/main.go  --depth=3  --delay=5s   http://localhost:8888
test:
	go test -count=1 ./... -v
	