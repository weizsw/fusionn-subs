.PHONY: build run test clean docker lint tidy

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X github.com/fusionn-subs/internal/version.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o fusionn-subs ./cmd/fusionn-subs

run:
	go run ./cmd/fusionn-subs

test:
	go test -v ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -f fusionn-subs

docker:
	docker build --build-arg VERSION=$(VERSION) -t fusionn-subs:$(VERSION) .

docker-run:
	docker compose up -d

docker-logs:
	docker compose logs -f

docker-stop:
	docker compose down
