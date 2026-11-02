---
title: ownscale.net
weight: 2
---

ownscale.net is the Ownscale instance for users to evaluate.
New features are first deployed on ownscale.net and the integration tests are
run on it. Backups and alerts are working on the instance, but there are no
quality of service guarantees.


## Funnel limitations

All ownscale features are free to use on ownscale.net except for funnels.
Because funnels expose user-provided content to the internet, there is a limit
for how many requests can be processed. That is 10 requests per second
per organization.
Additionally, requests bigger than 32MiB will not get served through funnels.
