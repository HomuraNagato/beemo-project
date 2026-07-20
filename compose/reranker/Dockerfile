ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM golang:1.23-bookworm AS build

WORKDIR /src

COPY compose/reranker/go.mod compose/reranker/go.sum* ./
RUN go mod download

COPY compose/reranker/main.go ./
RUN CGO_ENABLED=1 go build -o /out/eve-reranker .

FROM ${RUNTIME_IMAGE}

ARG ONNXRUNTIME_VERSION=1.26.0
ARG ONNXRUNTIME_PACKAGE=onnxruntime-linux-x64

ENV ONNXRUNTIME_SHARED_LIBRARY_PATH=/opt/onnxruntime/lib/libonnxruntime.so.1.26.0 \
    LD_LIBRARY_PATH=/opt/onnxruntime/lib

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/${ONNXRUNTIME_PACKAGE}-${ONNXRUNTIME_VERSION}.tgz" \
    | tar -xz -C /opt \
    && mv "/opt/${ONNXRUNTIME_PACKAGE}-${ONNXRUNTIME_VERSION}" /opt/onnxruntime

COPY --from=build /out/eve-reranker /usr/local/bin/eve-reranker

CMD ["eve-reranker"]
