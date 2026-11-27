---
title: Use an exit node
weight: 30
---

By default, a device on your tailnet sends only tailnet traffic through
Tailscale; everything else goes out over its own internet connection. An
**exit node** is a device on the tailnet that other devices can send all of
their internet traffic through instead. It's useful when:

- you're on an untrusted Wi-Fi network, such as in a café or hotel;
- you're traveling and need services that only work from your home country;
- you need to test a site or service from a specific location or IP address.

## How it works

Using an exit node is opt-in on both sides: a device must advertise itself
as an exit node, an admin must approve it in the console, and each device
chooses whether to use it. Traffic between the device and the exit node is
encrypted by WireGuard; from the exit node on, it leaves through the exit
node's internet connection, with the exit node's public IP address.

### Local network access

While using an exit node, a device can't reach its own local network, such
as a printer or NAS. Allowing local network access lets it, but also exposes
the device to the other devices on that network, which may be untrusted on
public Wi-Fi.

## Prerequisites

- An ownscale organization with at least two devices.
- The exit node runs Linux, macOS, Windows, Android or iOS, and stays online.
- If your access control policy doesn't allow all traffic, a rule that lets
  the devices use it; see [step 3](#allow-devices-to-use-it).

## Set up an exit node

{{% steps %}}

### Advertise a device as an exit node

{{< tabs >}}

{{< tab name="Linux" >}}
Enable IP forwarding:

```sh
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

Then advertise the device:

```sh
sudo tailscale set --advertise-exit-node
```

If the device uses `firewalld`, also allow masquerading:

```sh
sudo firewall-cmd --permanent --add-masquerade
```

Make sure the firewall denies forwarding by default, so the device only
forwards traffic from the tailnet. `ufw` and `firewalld` do.
{{< /tab >}}

{{< tab name="macOS" >}}
In the menu bar, open Tailscale and choose **Exit Node** > **Run Exit
Node**. If you run `tailscaled` from the command line, follow the Linux
steps instead.
{{< /tab >}}

{{< tab name="Windows" >}}
In the system tray, open Tailscale and choose **Exit node** > **Run exit
node**.
{{< /tab >}}

{{< tab name="Android" >}}
In the Tailscale app, open **Exit Node** and choose **Run as exit node**.
{{< /tab >}}

{{< tab name="iOS" >}}
In the Tailscale app, open **Exit Node** and choose **Run as Exit Node**.
{{< /tab >}}

{{< /tabs >}}

### Approve it in the console

On [Devices](/console/devices), the device now shows an **Exit node ·
awaiting approval** badge. Open its menu, choose **Edit route settings**,
tick **Use as exit node** and save.

To approve exit nodes automatically, list who may run them under
`autoApprovers` in the [access control policy](/console/access-control):

```json
"autoApprovers": {
  "exitNode": ["tag:exit", "alice@example.com"]
}
```

### Allow devices to use it

With the default policy, every device may use any approved exit node. If
your policy is more restrictive, add a rule with `autogroup:internet` as the
destination for the users or devices that may use exit nodes:

```json
{
  "action": "accept",
  "src": ["group:travelers"],
  "dst": ["autogroup:internet:*"]
}
```

A rule with the exit node itself as the destination only allows connections
to that device, such as SSH, not routing through it.

### Use the exit node

{{< tabs >}}

{{< tab name="Linux" >}}
```sh
sudo tailscale set --exit-node=<device>
```

`<device>` is the exit node's name or Tailscale IP address, as shown by
`tailscale status`. To also allow local network access:

```sh
sudo tailscale set --exit-node=<device> --exit-node-allow-lan-access=true
```

To stop using it:

```sh
sudo tailscale set --exit-node=
```
{{< /tab >}}

{{< tab name="macOS" >}}
In the menu bar, open Tailscale, choose **Exit Nodes** and pick the device.
Turn on **Allow Local Network Access** if needed. Choose **None** to stop.
{{< /tab >}}

{{< tab name="Windows" >}}
In the system tray, open Tailscale, choose **Exit node** and pick the
device. Turn on **Allow local network access** if needed. Choose **None**
to stop.
{{< /tab >}}

{{< tab name="Android" >}}
In the Tailscale app, open **Exit Node** and pick the device. Turn on
**Allow LAN access** if needed. Choose **None** to stop.
{{< /tab >}}

{{< tab name="iOS" >}}
In the Tailscale app, tap the exit node picker at the top and pick the
device. Choose **None** to stop.
{{< /tab >}}

{{< /tabs >}}

To check that it works, look up your public IP address with any "what is
my IP" site: it should be the exit node's.

{{% /steps %}}

## Caveats

- **Clients only offer approved exit nodes.** A device that advertises
  itself but hasn't been approved doesn't appear in the apps' exit node
  menus.
- **macOS, Windows and Android exit nodes route in userspace**, which is
  slower than Linux. Prefer a Linux device for heavy traffic.
- **The exit node must stay awake.** Disable sleep on macOS and Windows,
  enable **Run unattended** on Windows, and keep Android devices plugged in.
- **Key expiry.** When the exit node's key expires, devices using it lose
  internet access until it signs in again. Choose **Disable key expiry** in
  its menu on Devices, or register it with a tagged pre-auth key; see
  [Servers, containers and CI](/docs/use-cases/servers/).

See also Tailscale's [exit node
documentation](https://tailscale.com/docs/features/exit-nodes).
