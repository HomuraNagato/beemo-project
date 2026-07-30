FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY compose/reranker_proxy/go.mod ./
COPY compose/reranker_proxy/main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/reranker-proxy .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/reranker-proxy /usr/local/bin/reranker-proxy
ENTRYPOINT ["/usr/local/bin/reranker-proxy"]
