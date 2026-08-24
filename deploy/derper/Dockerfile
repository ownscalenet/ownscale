# syntax=docker/dockerfile:1
#
# Builds Tailscale's own "derper" DERP relay binary from source
#
# This image is meant to be deployed standalone, on its own server, separate
# from the rest of ownscale's docker-compose.yml stack. derper needs to own ports
# 80/443/3478 outright for its own Let's Encrypt cert and can't be reverse-proxied
# like Caddy does for every other ownscale service.

FROM golang:1.27-alpine AS build

# Pin to a specific tailscale.com release; bump deliberately, matching how
# regularly upstream tailscaled clients are updated (see cmd/derper's own
# README: "you should update this binary approximately as regularly as you
# update Tailscale nodes").
ARG TAILSCALE_VERSION=v1.102.3

WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go install "tailscale.com/cmd/derper@${TAILSCALE_VERSION}"

FROM alpine:3
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=build /go/bin/derper /app/derper

# DERP/HTTPS + HTTP (ACME http-01 challenge) + STUN.
EXPOSE 443 80 3478/udp

ENTRYPOINT ["/app/derper"]
