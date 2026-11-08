---
title: ownscale
layout: hextra-home
---

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
Self-hosted Mesh VPN
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  ownscale is a self-hosted coordination server and web console for Tailscale clients,&nbsp;<br class="hx:sm:block hx:hidden" />built on Headscale. Run many private networks from one server.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6 hx:flex hx:flex-wrap hx:gap-4">
{{< hextra/hero-button text="Try online" link="/login" >}}
{{< hextra/hero-button text="Self-host" link="/docs/self-hosting/installation/" >}}
{{< hextra/hero-button text="Read docs" link="/docs/" >}}
</div>

<div class="hx:mt-6"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Many tailnets, one server"
    subtitle="Each organization gets its own tailnet, backed by its own Headscale instance and subdomain."
  >}}
  {{< hextra/feature-card
    title="Web console"
    subtitle="Manage machines, users, access control, DNS and auth keys from the browser."
  >}}
  {{< hextra/feature-card
    title="Single sign-on and passkeys"
    subtitle="Log in with OIDC, Google, Microsoft, GitHub or a passkey."
  >}}
  {{< hextra/feature-card
    title="Funnels"
    subtitle="Expose a service running on a machine in your tailnet to the public internet."
  >}}
  {{< hextra/feature-card
    title="Tailscale features"
    subtitle="MagicDNS, split DNS, subnet routers, exit nodes, tags, ACLs, grants, Tailscale SSH, Taildrop and HTTPS certificates."
  >}}
  {{< hextra/feature-card
    title="Simple to operate"
    subtitle="One process, SQLite databases and continuous Litestream backups. Deployed with Docker Compose."
  >}}
{{< /hextra/feature-grid >}}
