---
title: SSH into machines
weight: 40
---

Tailscale SSH authenticates with your tailnet identity, so there are no SSH
keys to distribute.

1. Allow it in the `ssh` section of the [Access
   Control](/console/access-control) policy. For example, to let every user
   SSH into their own machines:

   ```jsonc
   "ssh": [
     {
       "action": "accept",
       "src": ["autogroup:member"],
       "dst": ["autogroup:self"],
       "users": ["autogroup:nonroot", "root"],
     },
   ],
   ```

   `src` and `dst` can't be `*`: use `autogroup:member`, `autogroup:self`,
   `autogroup:tagged`, groups, users or tags. The `acls` must also allow
   port 22 between the machines.

2. On each machine to SSH into, run `tailscale set --ssh`.
3. Connect with `ssh user@machine` from another machine in the tailnet.

## From the browser

On **Machines**, open a machine's menu and choose **SSH** to get a terminal
in the browser, with nothing installed. The browser joins the tailnet as you
for the session, so the same `ssh` rules apply.
