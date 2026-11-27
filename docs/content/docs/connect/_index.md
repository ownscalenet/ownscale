---
title: Connecting devices
weight: 40
aliases:
  - /docs/connecting-devices/
---

ownscale works with the official Tailscale clients. The only difference from
using Tailscale's hosted service is that each client must be told to use your
organization's **login server**, `https://<org>.<BASE_DOMAIN>`, instead of
Tailscale's servers. The [console](/console/) shows this URL in its header.

The console's **Devices → Add device → Client device** dialog shows the
steps for each platform with your organization's login server filled in.

Below, replace `https://myorg.example.com` with your own login server. To
register without a browser (for servers), use **Add device → Server**, or
create a key on [Pre-auth Keys](/console/pre-auth-keys), and add
`--authkey=<key>` to the command. To register a device that is already
showing a login URL to someone other than the person signing in on it, use
**Add device → Waiting device** and paste that URL.

## Windows

Install [Tailscale for Windows](https://tailscale.com/download/windows).
Windows has no menu option for a custom server, so open a Command Prompt or
PowerShell and run:

```sh
tailscale up --login-server=https://myorg.example.com
```

Open the URL it prints and sign in.

## Linux

Install [Tailscale](https://tailscale.com/download/linux), then run:

```sh
tailscale up --login-server=https://myorg.example.com
```

Open the URL it prints and sign in.

## macOS

Install [Tailscale for macOS](https://tailscale.com/download/mac). You can
either use the CLI or the menu bar app:

- **Command line**: the `tailscale` command isn't on your `PATH` by default.
  In the app, go to **Settings → Install CLI integration** first (one-time,
  requires an admin password), then run the same command as above.
- **Menu bar app**: option-click the Tailscale icon, open **Debug**, and
  under **Custom Login Server** choose **Add Account…**. Enter the login
  server URL and sign in.

## iOS

Install the
[Tailscale iOS app](https://apps.apple.com/app/tailscale/id1470499037). Tap
the account icon, then **Log in…**. In the top-right menu choose **Use
custom coordination server**, enter the login server URL and sign in.

## Android

Install the
[Tailscale Android app](https://play.google.com/store/apps/details?id=com.tailscale.ipn)
(or [F-Droid](https://f-droid.org/packages/com.tailscale.ipn/)). Open
**Settings** (top right), then **Accounts**. In the ⋮ menu choose **Use an
alternate server**, enter the login server URL and sign in.
