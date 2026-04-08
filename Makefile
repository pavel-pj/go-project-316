start:
	go run cmd/hexlet-go-crawler/main.go	

lint:
	golangci-lint run ./...
	
build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler/main.go	
	
run:
	@if [ -z "$(URL)" ]; then \
		echo "Error: URL is required. Usage: make run URL=<url>"; \
		exit 1; \
	fi
	go run cmd/hexlet-go-crawler/main.go $(URL)	

test:
	go test -count=1 ./... -v
	