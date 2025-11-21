ARG GO_VERSION=1.23.2
ARG PYTHON_IMAGE=python:3.11-slim
ARG LLM_SUBTRANS_REPO=https://github.com/machinewrapped/llm-subtrans.git

FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/fusionn-subs ./cmd/worker

FROM ${PYTHON_IMAGE} AS runtime
ARG LLM_SUBTRANS_REPO
ENV LLM_SUBTRANS_DIR=/opt/llm-subtrans \
    GEMINI_SCRIPT_PATH=/opt/llm-subtrans/gemini-subtrans.sh \
    GEMINI_WORKDIR=/opt/llm-subtrans

RUN apt-get update && apt-get install -y --no-install-recommends git build-essential && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 ${LLM_SUBTRANS_REPO} ${LLM_SUBTRANS_DIR}

WORKDIR ${LLM_SUBTRANS_DIR}

RUN set -e; printf "2\n\n2\n\n" | ./install.sh

WORKDIR /app
COPY --from=builder /out/fusionn-subs /usr/local/bin/fusionn-subs

ENTRYPOINT ["/usr/local/bin/fusionn-subs"]

