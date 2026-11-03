# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-X github.com/ownscalenet/ownscale/utils.Version=$VERSION" -o /out/ownscale ./cmd/ownscale && \
    CGO_ENABLED=0 go build -ldflags "-X github.com/ownscalenet/ownscale/utils.Version=$VERSION" -o /out/osa ./cmd/osa

FROM alpine:3
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=build /out/ownscale /out/osa /bin/
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/app/entrypoint.sh"]
