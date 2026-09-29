FROM ubuntu:latest

RUN apt-get update -qq && \
    apt-get install -y -qq ca-certificates curl dnsmasq-base iputils-ping openssh-client python3 vim && \
    curl -fsSL https://tailscale.com/install.sh | sh
COPY --chmod=0755 devbox-up /usr/local/bin/devbox-up
