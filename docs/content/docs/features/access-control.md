---
title: Access control
weight: 15
---

The access control policy decides which machines can talk to each other, on
which ports, and who may SSH where. Tailscale networks deny everything the
policy doesn't allow. Edit the policy on the **Access Control** page.

**Who can do this:** managers edit the policy. Members can view it.

## The policy file

The policy is a [HuJSON]({{< relref "/docs/concepts/terminology.md#hujson" >}})
document: JSON that allows comments and trailing commas. ownscale uses
Headscale's policy engine, which follows Tailscale's syntax. Its main
sections are:

| Section | Purpose |
| --- | --- |
| `groups` | Named lists of users, e.g. `"group:eng": ["alice@", "bob@example.com"]`. |
| `tagOwners` | Who may apply each [tag]({{< relref "tags.md" >}}), e.g. `"tag:server": ["group:eng"]`. |
| `hosts` | Names for IP addresses or ranges, e.g. `"office-lan": "192.168.1.0/24"`. |
| `acls` | Rules allowing traffic: `src`, `dst` as `host:port`, optional `proto`. |
| `grants` | Rules combining network access (`ip`) and application capabilities (`app`). |
| `ssh` | [Tailscale SSH]({{< relref "tailscale-ssh.md" >}}) rules. |
| `autoApprovers` | Users or tags whose advertised routes and exit nodes are approved automatically. |
| `nodeAttrs` | Extra attributes for machines, e.g. for [Taildrive]({{< relref "taildrop-and-taildrive.md" >}}). |
| `tests` | Assertions about who can reach what, checked on every save. |

Sources and destinations can be users (`alice@example.com`), groups,
tags, [autogroups]({{< relref "/docs/concepts/terminology.md#autogroup" >}}),
hosts, IP addresses or CIDR ranges, and `*` for everything.

For the full syntax see Headscale's
[policy reference](https://headscale.net/stable/ref/policy/) and Tailscale's
[ACL docs](https://tailscale.com/kb/1018/acls).

## The default policy

New organizations start with a policy that lets every machine reach every
other machine, and lets each user SSH into their own machines:

```jsonc
{
  "acls": [
    { "action": "accept", "src": ["*"], "dst": ["*:*"] },
  ],
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["autogroup:self"],
      "users": ["autogroup:nonroot", "root"],
    },
  ],
}
```

Replace the allow-all rule once the tailnet has more than a few trusted
users. See Tailscale's [ACL examples](https://tailscale.com/kb/1192/acl-samples).

## Editing safely

The editor has four buttons:

- **Format** re-indents the document.
- **Run tests** validates the policy and runs its `tests` without saving.
- **View diff** shows your changes against the saved policy.
- **Save** validates, runs the tests and applies the policy. A policy with
  syntax errors or failing tests isn't saved.

Changes reach connected machines within seconds, and no restart is needed.

## Policy tests

Tests protect against mistakes that lock people out or open too much. Each
test names a source and lists destinations that must be accepted or denied:

```jsonc
"tests": [
  {
    "src": "alice@example.com",
    "accept": ["tag:server:22"],
    "deny": ["tag:database:5432"],
  },
],
```

## Notes

- When a manager applies a new tag from the console, ownscale adds it to
  `tagOwners` automatically. See [Tags]({{< relref "tags.md" >}}).
- To reach a [funnel]({{< relref "funnels.md" >}}) target, the policy must
  allow traffic from the `ownscale-agent` machine to the target's port. The
  default allow-all rule does.
