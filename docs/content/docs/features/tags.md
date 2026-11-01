---
title: Tags
weight: 2
---

A tag, such as `tag:server` or `tag:ci`, is a label for a machine. Tags let
you write access rules about *kinds* of machines instead of individual users
or devices.

A tagged machine is owned by its tags, not by the user who set it up. This
has two consequences:

- The machine keeps working when that user is removed from the organization.
- Rules that match the user, such as `autogroup:self` or the user's email,
  no longer match the machine.

Use tags for servers, shared infrastructure and automation. Leave personal
devices untagged.

## Tagging a machine

There are three ways to tag a machine:

1. **From the console.** On **Machines**, open the machine's menu and choose
   **Tags**. Managers only.
2. **With an auth key.** Machines registered with a tagged
   [auth key]({{< relref "auth-keys.md" >}}) get its tags.
3. **From the client.** Run
   `tailscale up --advertise-tags=tag:server --login-server=…`. The user
   signing in must be listed for that tag in `tagOwners`.

## Tag names

The console normalizes tags you type: it lowercases them, adds the `tag:`
prefix if it's missing and removes duplicates. `Server` becomes
`tag:server`.

## tagOwners

Headscale only accepts tags declared in the policy's `tagOwners` section.
When a manager applies a tag that isn't declared yet, ownscale adds it to
`tagOwners` with every current user as an owner, so the change works right
away.

Edit that entry on **Access Control** if only some people should be able to
apply the tag from their clients:

```jsonc
"tagOwners": {
  "tag:server": ["group:ops"],
},
```

See Tailscale's [tags docs](https://tailscale.com/kb/1068/tags).
