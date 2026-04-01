
FROM golang:1.22-bullseye

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0

ENV PATH="/go/bin:${PATH}"

WORKDIR /workspace

ENTRYPOINT ["/bin/bash", "/workspace/scripts/eve_install.sh"]
