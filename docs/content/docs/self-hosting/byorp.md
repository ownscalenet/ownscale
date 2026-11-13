---
title: Bring your own reverse proxy
linkTitle: Own reverse proxy
weight: 3
---

The `ownscale` container serves everything on plain HTTP port `8080`: the
console, this documentation site, every organization's login server and every
funnel. The provided Caddy image only adds TLS. If you already run a reverse
proxy, you can use it instead.

The [install script]({{< relref "installation.md#install-script" >}}) sets
this up for Caddy, Traefik, nginx and Nginx Proxy Manager, and prints the
proxy config with your domain filled in.

## What to route

With `BASE_DOMAIN=example.com` and `CONTENT_DOMAIN=example-content.net`,
send these hosts to `ownscale:8080`:

| Host | Serves |
| --- | --- |
| `example.com` | This documentation site |
| `*.example.com` | The console, organizations' login servers |
| `example-content.net` | A short page explaining the content domain |
| `*.example-content.net` | Funnels |

Without a separate `CONTENT_DOMAIN`, the first two rows are enough.

Your proxy must:

- **keep the `Host` header**: ownscale routes by host name;
- **allow HTTP upgrades**: Tailscale clients upgrade `/ts2021` to their
  control protocol, and funnels can carry WebSockets;
- **not time out idle requests**: clients keep a long-poll open to their
  login server for as long as they're connected;
- **terminate TLS** with certificates for both wildcards, or [on
  demand](#on-demand-certificates). Wildcard certificates need an ACME
  DNS-01 challenge;
- **redirect HTTP to HTTPS**: ownscale sends HSTS, but only over HTTPS;
- **set `X-Forwarded-For`** to the client's address. ownscale only believes it
  from the addresses in `OWNSCALE_TRUSTED_PROXIES`, which `compose.yaml`
  defaults to the private ranges. Set it to your proxy's address otherwise.

ownscale compresses its responses itself, so don't enable compression in the
proxy.

Keep the [DNS provider]({{< relref "requirements.md#dns-provider-optional" >}})
settings in `.env` anyway, if you have one: ownscale uses them to issue machine
certificates.

## Run ownscale without Caddy

Follow the [installation]({{< relref "installation.md" >}}) steps, but set
`COMPOSE_PROFILES=` (empty) in `.env`, so the bundled Caddy doesn't start.
Then let your proxy reach ownscale with a `compose.override.yaml` next to
`compose.yaml`. For a proxy container, join its Docker network, so it reaches
`ownscale:8080`:

```yaml
services:
  ownscale:
    networks: [default, proxy]
networks:
  proxy:
    name: my-proxy-network # see `docker network ls`
    external: true
```

For a proxy running on the host, publish the port on loopback instead and use
`127.0.0.1:8080`:

```yaml
services:
  ownscale:
    ports:
      - "127.0.0.1:8080:8080"
```

## Caddy

Caddy can get a certificate for each host [on
demand](#on-demand-certificates), so it needs no DNS provider:

```caddy
{
	on_demand_tls {
		ask http://ownscale:8081/tls-allowed
	}
}

example.com, *.example.com, example-content.net, *.example-content.net {
	tls {
		on_demand
	}
	reverse_proxy ownscale:8080
}
```

For Caddy on the host, also publish `127.0.0.1:8081:8081` and use
`127.0.0.1` instead of `ownscale`.

## nginx

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name example.com .example.com example-content.net .example-content.net;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name example.com .example.com example-content.net .example-content.net;

    # One certificate covering example.com and *.example.com, one for the
    # content domain. nginx picks by SNI.
    ssl_certificate     /etc/ssl/example.com/fullchain.pem;
    ssl_certificate_key /etc/ssl/example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 1d;
        proxy_send_timeout 1d;
    }
}
```

## Traefik

Attach the `ownscale` service to Traefik's network and label it. The
`certresolver` must use a DNS challenge. `traefik.docker.network` tells
Traefik which of ownscale's networks to use.

```yaml
services:
  ownscale:
    networks: [default, traefik]
    labels:
      traefik.enable: "true"
      traefik.docker.network: traefik
      traefik.http.routers.ownscale.rule: >-
        Host(`example.com`) || HostRegexp(`^[a-z0-9-]+\.example\.com$$`) ||
        Host(`example-content.net`) || HostRegexp(`^[a-z0-9-]+\.example-content\.net$$`)
      traefik.http.routers.ownscale.entrypoints: websecure
      traefik.http.routers.ownscale.tls.certresolver: letsencrypt
      traefik.http.routers.ownscale.tls.domains[0].main: example.com
      traefik.http.routers.ownscale.tls.domains[0].sans: "*.example.com"
      traefik.http.routers.ownscale.tls.domains[1].main: example-content.net
      traefik.http.routers.ownscale.tls.domains[1].sans: "*.example-content.net"
      traefik.http.services.ownscale.loadbalancer.server.port: "8080"

networks:
  traefik:
    external: true
```

Compose needs `$` written as `$$`. Traefik v3 times out requests after 60
seconds by default. Set
`entryPoints.websecure.transport.respondingTimeouts.readTimeout` to `0`.
Redirect the `web` entry point to `websecure` if you haven't already.

## Nginx Proxy Manager

Join ownscale to NPM's Docker network as shown
[above](#run-ownscale-without-caddy), then add a proxy host:

- **Details**: domain names `example.com` and `*.example.com` (plus the
  content domain's), forward to `http` `ownscale` port `8080`, and turn on
  *Websockets Support*.
- **SSL**: request a new certificate with *Use a DNS Challenge*, which
  wildcards need, and turn on *Force SSL* and *HTTP/2 Support*.
- **Advanced**, custom nginx configuration:

  ```nginx
  proxy_buffering off;
  proxy_read_timeout 1d;
  proxy_send_timeout 1d;
  ```

## On-demand certificates

If your proxy can't do DNS-01 but can request certificates on demand (Caddy's
`on_demand_tls`), point its ask URL at `http://ownscale:8081/tls-allowed`,
which answers 200 for host names ownscale serves. `compose.yaml` sets
`OWNSCALE_TLS_ASK_ADDR` to `:8081`. It reveals which organizations exist, so
publish that port on loopback at most.
