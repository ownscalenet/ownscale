---
title: Use an exit node
weight: 30
---

An exit node sends all of a device's internet traffic through another
machine in the tailnet, for example to use your home connection from public
Wi-Fi.

1. On the machine that will be the exit node:

   ```sh
   tailscale set --advertise-exit-node
   ```

2. In the console, open that machine's **Edit route settings** and tick **Use
   as exit node**.
3. On the device that should use it, pick it in the Tailscale app's **Exit
   node** menu, or run:

   ```sh
   tailscale set --exit-node=<machine>
   ```

If your access control policy doesn't allow all traffic, add a rule with
`autogroup:internet:*` as a destination for the devices that may use it.
