---
title: Users and roles
weight: 10
---

Every ownscale account belongs to one [organization]({{< relref "/docs/concepts/organizations.md" >}})
and has one role in it. The **Users** page lists the members, their roles and
the pending [invites]({{< relref "invites.md" >}}).

## Roles

| Role | Can do |
| --- | --- |
| `owner` | Everything an admin can, plus transfer ownership. Each organization has exactly one owner. |
| `admin` | Manage members, invites, the access control policy, DNS, auth restrictions, funnels, tags and route approvals. Manage any machine and create auth keys for anyone, including tag-only keys. |
| `member` | Add machines for themselves. Rename, expire and remove their own machines. Create untagged auth keys for themselves. View the policy, DNS and auth restrictions read-only. |

The documentation calls `owner` and `admin` **managers**.

{{< callout type="warning" >}}
The console also offers the `network_admin`, `it_admin` and `auditor`
roles. They are reserved for future use and currently have exactly the same
permissions as `member`.
{{< /callout >}}

## Changing a role

A manager can change any member's role on the **Users** page. The owner role
can't be given this way, nor through an invite: use **Transfer ownership**.

## Transferring ownership

Only the owner can transfer ownership. The chosen member becomes the owner,
and the previous owner becomes an `admin`.

## Removing a member

A manager can remove any member except the owner. Removing a member:

- deletes their ownscale account;
- deletes their Headscale user and **all their machines**, which drop off the
  tailnet immediately.

Tagged machines aren't owned by a user, so they stay. If the removed person
signs in again later, they get a fresh account and a new organization of
their own, unless they use a new invite.

## Users in the policy file

Refer to a member in the policy file by their email address, such as
`alice@example.com`, or by their Headscale username followed by `@`, such as
`alice@`. The username is the part of the email before the `@`, made unique
within the organization. Accounts created with a passkey have the email
`<username>@passkey`. Use these in `groups`, `acls`, `grants` and `ssh`
rules. See [Access control]({{< relref "access-control.md" >}}).
