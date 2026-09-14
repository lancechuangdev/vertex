FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/indexer ./cmd/indexer

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S vertex \
    && adduser -S -G vertex -u 10001 vertex
COPY --from=build /out/indexer /usr/local/bin/indexer
USER vertex
EXPOSE 9090
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:9090/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/indexer"]
CMD ["run"]
