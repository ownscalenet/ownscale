---
title: Connecting devices
weight: 30
aliases:
  - /docs/connecting-devices/
---

ownscale works with the official Tailscale clients. The only difference from
using Tailscale's hosted service is that each client must be told to use your
organization's **login server**, `https://<org>.<BASE_DOMAIN>`, instead of
Tailscale's servers. The [console](/console/) shows this URL in its header.

Install the [Tailscale client](https://tailscale.com/download), then point it
at your login server. Below, replace `https://myorg.example.com` with yours.
You can also add a machine from **Machines → Register machine** in the
console, which shows the command for your organization.

## Linux, macOS and Windows (command line)

```sh
tailscale up --login-server=https://myorg.example.com
```

Open the URL it prints and sign in. To register without a browser, for
servers, create a key on [Pre-auth Keys](/console/pre-auth-keys) and add
`--authkey=<key>`.

## macOS (menu bar app)

Option-click the Tailscale icon, open **Debug**, and under **Custom Login
Server** choose **Add Account…**. Enter the login server URL and sign in.

## iOS

Tap the account icon, then **Log in…**. In the top-right menu choose **Use
custom coordination server**, enter the login server URL and sign in.

## Android

Open **Settings** (top right), then **Accounts**. In the ⋮ menu choose **Use
an alternate server**, enter the login server URL and sign in.
