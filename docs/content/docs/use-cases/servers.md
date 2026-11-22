---
title: Servers, containers and CI
weight: 70
---

Machines without a browser register with a **pre-auth key** instead of
signing in.

1. On [Pre-auth Keys](/console/pre-auth-keys), choose **Create pre-auth key**:
   - **Reusable** to register many machines with one key;
   - **Ephemeral** for short-lived machines, such as CI runners and
     containers: they're removed shortly after going offline;
   - **Tag only** with ACL tags, so the machines belong to the tags rather
     than to a user.
2. Register the machine:

   ```sh
   tailscale up --login-server=https://myorg.example.com --authkey=<key>
   ```

Servers' node keys expire like any machine's, and they then need to sign in
again. To avoid that, choose **Disable key expiry** in the machine's menu on
**Machines**. Tagged machines' keys don't expire.

## GitHub Actions

The [Tailscale GitHub Action](https://github.com/tailscale/github-action)
connects a workflow's runner to your tailnet, so jobs can reach private
machines, for example to deploy to a server or run tests against a database.
It works with ownscale when you pass a pre-auth key and your organization's
login server.

1. Create a pre-auth key that is **Reusable**, so every run can use it, and
   **Ephemeral**, so runners are removed once the job ends. Choose **Tag
   only** with a tag such as `tag:ci`, and allow that tag what the job needs
   in the [access control policy](/console/access-control).
2. In the GitHub repository, add the key as an Actions secret named
   `OWNSCALE_AUTHKEY`.
3. Add the action to the workflow, with your login server in `args`:

   ```yaml
   jobs:
     deploy:
       runs-on: ubuntu-latest
       steps:
         - uses: tailscale/github-action@v4
           with:
             authkey: ${{ secrets.OWNSCALE_AUTHKEY }}
             args: --login-server=https://myorg.ownscale.net
         - run: ssh deploy@myserver ./deploy.sh
   ```

Use `authkey` only. The action's `oauth-client-id` and `oauth-secret` inputs
rely on Tailscale's own OAuth clients, which ownscale doesn't have. Leave
`tags` unset as well: the key's tags already apply to the runner.
