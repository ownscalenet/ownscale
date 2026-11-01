---
title: Auth keys
weight: 14
---

An auth key (Headscale and the console call it a *pre-auth key*) registers a
machine without opening a browser:

```sh
sudo tailscale up --login-server=https://<org>.<BASE_DOMAIN> --authkey <key>
```

Use auth keys for servers, containers, CI jobs and anything else without a
person at the keyboard. Manage them on the **Pre-auth Keys** page.

## Key options

| Option | Effect |
| --- | --- |
| **User** | The member who will own machines registered with the key. |
| **Tag only** | The key has no owner. Machines registered with it get the key's [tags]({{< relref "tags.md" >}}) and are owned by them. |
| **Tags** | Managers can add [tags]({{< relref "tags.md" >}}) to a key for a user too. Machines registered with it are tagged. |
| **Expires in (days)** | Defaults to 90. After this many days (at least 1), the key can't register new machines. Machines already registered keep working. |
| **Reusable** | The key can register any number of machines. Otherwise it's spent after one. |
| **Ephemeral** | Machines registered with the key are removed automatically after about 2 minutes offline. |

After creating a key, the console shows it together with the
`tailscale up` command to use it. Copy the key while it is shown.

## Who can do what

- **Members** can create keys for themselves only, without tags, and can
  expire or delete their own keys.
- **Managers** can create keys for any member, keys with tags and tag-only
  keys, and can expire or delete any key.

## Expiring and deleting

**Expire** stops a key from registering more machines. **Delete** removes it
from the list. Neither affects machines already registered with it. To remove
those, remove them on the **Machines** page.

## Choosing the right key

| Use case | Key |
| --- | --- |
| One server set up by hand | single-use, tag only (`tag:server`) |
| A fleet of servers from a provisioning tool | reusable, tag only, short expiry |
| CI jobs or short-lived containers | reusable, ephemeral, tag only |
| A personal device without a browser | single-use, your own user |

See Tailscale's [auth key docs](https://tailscale.com/kb/1085/auth-keys).
