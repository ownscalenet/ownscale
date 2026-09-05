# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/ownscale ./cmd/ownscale && \
    CGO_ENABLED=0 go build -o /out/osa ./cmd/osa

FROM alpine:3
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=build /out/ownscale /out/osa /app/
# We need to verify database status against remote replication destinations.
COPY --from=litestream/litestream:0.5.16 /usr/local/bin/litestream /usr/local/bin/litestream
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

EXPOSE 80

ENTRYPOINT ["/app/entrypoint.sh"]
