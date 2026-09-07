FROM ubuntu:latest

RUN apt-get update -qq && \
    apt-get install -y -qq ca-certificates curl python3 && \
    curl -fsSL https://tailscale.com/install.sh | sh

