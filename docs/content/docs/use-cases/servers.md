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
