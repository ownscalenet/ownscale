---
title: Architecture
weight: 3
---

### Funnels create the agent machine

Ownscale funnels are an experimental feature that will change in the future.
Today, funnels are implemented with the help of a lightweight client joining
in the user tailnet and relaying data to the internet exposed endpoint.
A funnel can only be created from the web console, not by using the tailscale
CLI.

### Database

ownscale uses a built-in SQLite database and runs in-process
[Litestream](https://litestream.io) replication to a remote location
such as an S3-compatible backend. With replication being done on regular
interval, there is a possibility of the server dying without fully
[replicating latest comitted changes](https://litestream.io/tips/#data-loss-window).
It's a drawback we accept in order to get a fast database without
having to manage clusters and more complicated replication schemes.
