FROM golang:1.22-bullseye AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.1

COPY proto ./proto
COPY src/orchestrator ./src/orchestrator

RUN CGO_ENABLED=0 go build -trimpath -o /out/eve-orchestrator ./src/orchestrator

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/eve-orchestrator /usr/local/bin/eve-orchestrator
COPY --from=build /out/grpcurl /usr/local/bin/grpcurl

ENV PATH=/usr/local/bin
WORKDIR /workspace

CMD ["/usr/local/bin/eve-orchestrator"]
