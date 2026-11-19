---
title: Separate tailnets
weight: 80
---

One ownscale server can host many organizations, each with its own tailnet,
users, policy and login server at `https://<org>.<BASE_DOMAIN>`. Use it to
give each team, client or family its own network from one server.

- **Creating one**: anyone who signs in without an invitation creates a new
  organization and becomes its owner.
- **Adding people**: owners and admins invite them from
  [Invitations](/console/invitations), choosing their role.
- **Limiting it**: `OWNSCALE_MAX_ORGS` in `.env` caps how many organizations
  the server allows. Set it to `1` to keep a server to yourself.

Organizations are isolated: machines in one can't reach another's, and an
account belongs to one organization.
