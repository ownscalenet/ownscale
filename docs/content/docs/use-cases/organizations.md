---
title: Separate tailnets
weight: 80
---

One ownscale server can host many organizations, each with its own tailnet,
users, policy and login server. Use it to give each team, client or
family its own network from one server.
Organizations are isolated: devices in one can't reach another's, and an
account belongs to one organization.
Each organization has two URLs associated with it:
- **login URL**: it's the URL users send to the Tailscale client
  when logging in. It contains an organization label generated randomly,
  as in: `https://org_label.<BASE_DOMAIN>`.
- **content URL**: it's the URL where user-provided content such as
  funnels is served at. It too contains a label generated randomly,
  but is usually served from a secondary, content domain, as in:
  `https://label.<CONTENT_DOMAIN>`. See
  [Content domain](self-hosting/content-domain/) for more information.


Organizations can be interacted with in three ways:
- **Limiting them**: `OWNSCALE_MAX_ORGS` in `.env` caps how many organizations
  the server allows. Set it to `1` to keep a server to yourself. Check
  [`example.env`](https://github.com/ownscalenet/ownscale/blob/master/example.env).
- **Creating one**: anyone who signs in without an invitation creates a new
  organization and becomes its owner.
- **Adding people**: owners and admins invite them from
  [Invitations](/console/invitations), choosing their role.


> [!INFO]
> If an user accidentally creates their own organization by signing up
> without an invitation, the account can be deleted and re-created by
> signing up with an invitation from their admin.
