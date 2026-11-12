---
title: ownscale.net
weight: 2
---

[ownscale.net](https://ownscale.net) is a free Ownscale instance for users
to evaluate. New features are first deployed on ownscale.net and the
integration tests are run on it. Backups and alerts are working on
the instance, but there are no quality of service guarantees.
This instance exists only for evaluation purposes; don't rely on it
for anything important.

## Limitations

Users, machines, ACL settings, auth methods are all free to use, however,
features based on user-provided content are limited to prevent abuse.
Current limitations include:
- funnels support up to 10 requests per second
- funnel requests bigger than 32MiB will not get served
- getting TLS certificates for a machine, by using `tailscale cert` and
`tailscale serve` is limited to 10 certificates per week
