start:
	go run cmd/hexlet-go-crawler/main.go	

lint:
	golangci-lint run ./...
	
build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler/main.go	
	
run:
	go run cmd/hexlet-go-crawler/main.go --depth=5 --rps=10 --retries=2 --indent=true http://localhost:8888
test:
	go test -count=1 ./... -v
	