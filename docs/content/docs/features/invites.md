---
title: Invites
weight: 11
---

Invites bring other people into your organization. Without an invite, a
person who signs in gets their own new organization.

**Who can do this:** managers.

## Creating an invite

On **Users**, create an invite and choose:

- **Role**: any role except `owner`. Defaults to `member`.
- **Email** (optional): a note to remember who the invite is for. ownscale
  doesn't send email and doesn't check that the person accepting it has this
  address.

ownscale shows a link of the form `https://console.<BASE_DOMAIN>/invite/<token>`.
Send it to the person yourself, through a channel you trust.

## Accepting an invite

Opening the link shows which organization the invite is for. The person
signs in with any available method, and their account is created in your
organization with the invite's role.

Invites:

- are valid for **7 days**;
- can be used **once**;
- can be revoked from the **Users** page while pending.

{{< callout type="info" >}}
An invite only works for a new account. Someone who already belongs to
another organization must first be removed from it, since each account
belongs to exactly one organization.
{{< /callout >}}
