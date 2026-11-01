---
title: Login and passkeys
weight: 12
---

ownscale doesn't store passwords. Users sign in to the console, and to
Tailscale clients, with an external identity provider or a passkey.

## Sign-in methods

The login page at `https://console.<BASE_DOMAIN>/login` shows up to five
options. A button is greyed out when the server administrator hasn't
configured it.

| Method | Configured with |
| --- | --- |
| **Sign in with SSO** | any OpenID Connect provider: Okta, Keycloak, Authentik, Dex, Google Workspace, Entra ID, … (`OWNSCALE_AUTH_UPSTREAM_OIDC_*`) |
| **Sign in with Google** | a Google OAuth client (`OWNSCALE_AUTH_GOOGLE_*`) |
| **Sign in with Microsoft** | a Microsoft Entra app registration (`OWNSCALE_AUTH_MICROSOFT_*`) |
| **Sign in with GitHub** | a GitHub OAuth app (`OWNSCALE_AUTH_GITHUB_*`) |
| **Sign in with passkey** | always available |

Members of the same organization can use different methods. The server
administrator enables providers in `example.env`.

## Passkeys

A passkey is a key stored in your device, password manager or security key,
unlocked with a fingerprint, face or PIN. To create an account with a
passkey, open the **Sign up** page, choose a username and create the
passkey. Usernames are 2 to 32 characters long, start with a letter and may
contain letters, digits, `.`, `-` and `_`.

On **User settings** you can add more passkeys to your account, for example
one per device, and delete old ones. Any account can add passkeys, including
accounts that first signed in with SSO.

## Signing in on a Tailscale client

When `tailscale up` prints a login URL, it points at your organization's
Headscale instance, which sends you to ownscale to sign in. Sign in with the
same method you use for the console. The machine is then registered to your
account.

Managers can limit which accounts may do this with
[auth restrictions]({{< relref "auth-restrictions.md" >}}).
