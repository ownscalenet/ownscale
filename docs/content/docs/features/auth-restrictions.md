---
title: Auth restrictions
weight: 13
---

Auth restrictions are an allowlist that limits which accounts can sign in
to your organization's login server, the interactive login started by
`tailscale up`. They map to Headscale's OIDC `allowed_domains`,
`allowed_groups` and `allowed_users` settings.

**Who can do this:** managers edit them on the **Auth Restrictions** page.
Members can view them.

## Format

The allowlist is a [HuJSON]({{< relref "/docs/concepts/terminology.md#hujson" >}})
object with three optional lists:

```jsonc
{
  // Email domains, matched against the account's email.
  "domains": ["example.com"],
  // Groups reported by the identity provider.
  "groups": ["group:eng"],
  // Individual email addresses.
  "users": ["alice@example.com"],
}
```

- A login is allowed if it matches **any** entry in any list.
- If all three lists are empty, or omitted, every member of the organization
  can sign in.

{{< callout type="warning" >}}
Saving restarts the organization's Headscale instance. Connected machines
reconnect within seconds, and the funnel connection is re-established.
{{< /callout >}}

## When to use them

- Allow only company addresses, with `domains`, even if someone is invited
  with a personal account by mistake.
- Temporarily stop new machine logins by allowing a single address.

Restrictions don't affect machines registered with an
[auth key]({{< relref "auth-keys.md" >}}), and don't sign out machines that
are already connected.
