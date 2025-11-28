# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git for version info
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with version info
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/fusionn-subs/internal/version.Version=${VERSION}" \
    -o fusionn-subs ./cmd/fusionn-subs

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/fusionn-subs .

# Create config directory
RUN mkdir -p /app/config

ENV ENV=production
ENV CONFIG_PATH=/app/config/config.yaml

ENTRYPOINT ["/app/fusionn-subs"]
