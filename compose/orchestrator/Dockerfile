FROM golang:1.22-bullseye

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

ENV GOPATH=/go
ENV PATH="${GOPATH}/bin:${PATH}"

WORKDIR /workspace

RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

#RUN go mod tidy
