FROM ubuntu:latest

sudo apt install
RUN apt-get update -qq && \
    apt-get install -y -qq ca-certificates curl iputils-ping python3 vim && \
    curl -fsSL https://tailscale.com/install.sh | sh
