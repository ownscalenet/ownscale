---
title: Connect clients
weight: 2
---

ownscale works with the official Tailscale clients. The only difference from
using Tailscale's hosted service is that each client must be told to use your
organization's **login server**, `https://<org>.<BASE_DOMAIN>`, instead of
Tailscale's servers. The console shows this URL in its header.

Install the client for your platform from
[tailscale.com/download](https://tailscale.com/download), then point it at
the login server as shown below.

{{< tabs >}}
{{< tab name="Linux" >}}
```sh
sudo tailscale up --login-server=https://abcd.example.com
```
Open the printed URL and sign in, or use an
[auth key]({{< relref "/docs/features/auth-keys.md" >}}) with `--authkey`.
{{< /tab >}}
{{< tab name="macOS" >}}
With the CLI:
```sh
tailscale up --login-server=https://abcd.example.com
```
The menu bar app can also add an account on a custom server. Headscale's
[Apple guide](https://headscale.net/stable/usage/connect/apple/) shows where
to find the option.
{{< /tab >}}
{{< tab name="Windows" >}}
From PowerShell or Command Prompt:
```sh
tailscale up --login-server=https://abcd.example.com
```
For other options, see Headscale's
[Windows guide](https://headscale.net/stable/usage/connect/windows/).
{{< /tab >}}
{{< tab name="iOS / Android" >}}
The mobile apps can use a custom coordination server. Where to find the
setting changes between app versions; see Headscale's
[Apple](https://headscale.net/stable/usage/connect/apple/) and
[Android](https://headscale.net/stable/usage/connect/android/) guides.
{{< /tab >}}
{{< /tabs >}}

## Ways to authorize a machine

A machine joins the tailnet in one of three ways:

1. **Interactive sign-in.** `tailscale up` prints a URL. Open it and sign in
   with your ownscale account. The machine is owned by you.
2. **Register from the console.** Click **Register machine** on the
   **Machines** page, paste the registration key or URL printed by
   `tailscale up`, and pick the member who will own the machine. Use this
   for headless machines where opening a browser is impractical.
3. **Auth key.** Create a key on **Pre-auth Keys** and run
   `tailscale up --login-server=… --authkey <key>`. Nothing is opened in a
   browser. Best for servers, containers and CI. See
   [Auth keys]({{< relref "/docs/features/auth-keys.md" >}}).

{{< callout type="info" >}}
Each organization has its own login server. A client logged in to one
organization can't see machines in another. To switch, log out
(`tailscale logout`) and log in to the other login server.
{{< /callout >}}

For CLI flags beyond `--login-server` and `--authkey`, see Tailscale's
[CLI reference](https://tailscale.com/kb/1080/cli).
