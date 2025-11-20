PROJECT=fusionn-subs
BIN_DIR=bin
BIN=$(BIN_DIR)/$(PROJECT)

.PHONY: all build test lint tidy clean

all: build

build: $(BIN)

$(BIN):
	mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/worker

test:
	go test ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)

