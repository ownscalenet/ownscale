---
title: Ownscale
layout: hextra-home
---

<div class="ownscale-hero">

<div class="hx:mb-6">
{{< hextra/hero-headline >}}
Self-hosted Mesh VPN
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-10">
{{< hextra/hero-subtitle >}}
  Ownscale is a self-hosted coordination server and web console for Tailscale clients,
  built on Headscale. Run many private networks from one server.
{{< /hextra/hero-subtitle >}}
</div>

<div class="ownscale-hero-buttons">
{{< hextra/hero-button text="Try online" link="/login" >}}
<a class="ownscale-btn-outline not-prose" href="/docs/self-hosting/installation/">Self-host</a>
<a class="ownscale-btn-outline not-prose" href="/docs/">Read docs</a>
</div>
</div>

<div class="ownscale-features">
{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Many networks, one server"
    icon="collection"
    subtitle="Host multiple isolated networks from the same backend."
    link="/docs/use-cases/organizations/"
    class="ownscale-feature-accent"
  >}}
  {{< hextra/feature-card
    title="Web console"
    icon="view-grid"
    subtitle="Manage devices, users, access control, DNS and auth keys from the browser. Sign in with SSO or a passkey."
    link="/docs/about/"
  >}}
  {{< hextra/feature-card
    title="Stock Tailscale clients"
    icon="device-mobile"
    subtitle="Use the official Tailscale clients; just point them at your organization's login server."
    link="/docs/connect/"
    class="ownscale-feature-accent"
  >}}
  {{< hextra/feature-card
    title="Funnels"
    icon="globe-alt"
    subtitle="Expose the local running HTTP service to the internet."
    link="/docs/use-cases/funnels/"
  >}}
  {{< hextra/feature-card
    title="Tailscale features"
    icon="sparkles"
    subtitle="Taildrop, ACLs, grants, SSH, exit nodes, DNS, user roles and many more."
    link="/docs/about/how-it-works/#data"
    class="ownscale-feature-accent"
  >}}
  {{< hextra/feature-card
    title="Install in minutes"
    icon="server"
    subtitle="One script sets up Docker Compose, TLS and the reverse proxy on your server."
    link="/docs/self-hosting/installation/"
  >}}
{{< /hextra/feature-grid >}}
</div>
