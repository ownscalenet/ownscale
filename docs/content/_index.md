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

<div class="content hx:mt-6 hx:w-full">

## Goals

Ownscale goals build up on Headscale's goals of creating an open source
alternative to Tailscale.
Ownscale provides a way for self-hosters to run several tailnets from the same
backend and interact with them through an web UI.

To reduce deployment complexities, ownscale comes as a self-contained
binary that runs behind a reverse-proxy without the need of a database
cluster. We do have a database with backup&restore functions however.

## Features

**Many tailnets, one server**\
Organizations are used for splitting users into groups. Each organization
gets its own tailnet, backed by its own Headscale instance and subdomain.
Users from one organization can only access resources from their organization.

**Web console**\
Manage machines, users, access control, DNS and auth keys from the browser.

**Single sign-on and passkeys**\
Log in with OIDC, Google, Microsoft, GitHub or a passkey.

**Funnels**\
Expose a service running on a machine in your tailnet to the public internet.

**Tailscale features**\
MagicDNS, split DNS, subnet routers, exit nodes, tags, ACLs, grants,
Tailscale SSH, Taildrop and HTTPS certificates.

</div>
