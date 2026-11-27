---
title: Documentation
aliases:
  - /docs/get-started/
  - /docs/getting-started/
weight: 10
---

Ownscale is a self-hosted coordination server and web console for Tailscale
clients, built on [Headscale](https://headscale.net). It runs many private
networks from one server.

## Using these docs

Where to start depends on what you want to do. To try Ownscale without
running anything, start with [ownscale.net](/docs/ownscalenet/). To run it
on your own server, read [Self-hosting](/docs/self-hosting/), which covers
the requirements and installation. Once you have an organization, use
[Connecting devices](/docs/connect/) to add your devices, then
[Use cases](/docs/use-cases/) for setups such as exit nodes, SSH and
funnels.

Use the search box at the top of every page to find a page by keyword,
and the sidebar to move between sections. [Terminology](/docs/reference/terminology/)
explains the words these docs use, such as organization and login server, and
the [Reference](/docs/reference/) section lists the CLI commands and settings.
Every page has buttons next to its title to view or edit its source on
GitHub. If something is wrong or missing, open a pull request or an issue.

{{< cards >}}
  {{< card link="/docs/about/" title="About" icon="information-circle" subtitle="What Ownscale is and how it works" >}}
  {{< card link="/docs/self-hosting/" title="Self-hosting" icon="server" subtitle="Run Ownscale on your own server" >}}
  {{< card link="/docs/ownscalenet/" title="ownscale.net" icon="play" subtitle="Try it without hosting it" >}}
  {{< card link="/docs/connect/" title="Connecting devices" icon="device-mobile" subtitle="Point Tailscale clients at your login server" >}}
  {{< card link="/docs/use-cases/" title="Use cases" icon="light-bulb" subtitle="Home access, exit nodes, SSH, funnels and more" >}}
  {{< card link="/docs/reference/" title="Reference" icon="document-text" subtitle="CLI, settings and terminology" >}}
  {{< card link="/docs/faqs/" title="FAQs" icon="question-mark-circle" subtitle="Answers to common questions" >}}
{{< /cards >}}
