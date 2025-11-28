# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X github.com/fusionn-subs/internal/version.Version=${VERSION}" \
    -o fusionn-subs ./cmd/fusionn-subs

# Runtime stage
FROM python:3.11-slim

ENV LLM_SUBTRANS_DIR=/opt/llm-subtrans \
    GEMINI_SCRIPT_PATH=/opt/llm-subtrans/gemini-subtrans.sh \
    GEMINI_WORKDIR=/opt/llm-subtrans

RUN apt-get update && apt-get install -y --no-install-recommends git build-essential && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 https://github.com/machinewrapped/llm-subtrans.git ${LLM_SUBTRANS_DIR}

WORKDIR ${LLM_SUBTRANS_DIR}

RUN set -e; printf "2\n\n2\n\n" | ./install.sh

WORKDIR /app

COPY --from=builder /app/fusionn-subs .

ENV ENV=production
ENV CONFIG_PATH=/app/config/config.yaml

ENTRYPOINT ["/app/fusionn-subs"]
