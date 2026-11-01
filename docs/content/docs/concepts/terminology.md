---
title: Terminology
weight: 2
---

Tailscale and Headscale use a lot of specific vocabulary. This page explains
each term and where it shows up in ownscale. Terms are in alphabetical order.

## ACL

An *access control list* is a rule in the
[policy file](#policy-file) that allows traffic from a set of sources to a set
of destinations and ports, for example "group:dev may reach tag:server on
port 22". Tailscale networks deny everything that isn't explicitly allowed.
Edit ACLs on the **Access Control** page. See
[Access control]({{< relref "/docs/features/access-control.md" >}}) and
Tailscale's [ACL docs](https://tailscale.com/kb/1018/acls).

## Auth key

Also called a *pre-auth key*. A secret that registers a machine without an
interactive sign-in: `tailscale up --authkey <key>`. Keys can be
[reusable](#reusable-key), [ephemeral](#ephemeral-node), tied to a user or
to [tags](#tag), and they expire. Create them on **Pre-auth Keys**. See
[Auth keys]({{< relref "/docs/features/auth-keys.md" >}}).

## Autogroup

A built-in group the policy file fills in automatically:

| Autogroup | Means |
| --- | --- |
| `autogroup:member` | machines owned by users (not tagged) |
| `autogroup:tagged` | machines with at least one tag |
| `autogroup:self` | as a destination: the source user's own machines |
| `autogroup:internet` | internet access through an [exit node](#exit-node) |
| `autogroup:nonroot` | in SSH rules: any local user except `root` |

## Auto approvers

A policy section (`autoApprovers`) that approves [subnet routes](#subnet-router)
or [exit nodes](#exit-node) automatically when they are advertised by
specific users or tags, so a manager doesn't have to approve each one by
hand.

## Control plane

The part of the system that manages identities, keys, the network map and
policy. In ownscale it is the [coordination server](#coordination-server).
The opposite is the [data plane](#data-plane). See
[How it works]({{< relref "how-it-works.md" >}}).

## Coordination server

Also *control server*. The server Tailscale clients connect to in order to
log in, learn about their peers and receive policy. Tailscale's hosted
service runs its own. With ownscale it is a [Headscale](#headscale) instance,
one per [organization](#organization), reached at the [login server](#login-server).

## Data plane

The encrypted WireGuard traffic that flows directly between machines, or
through a [DERP](#derp) relay. It never goes through the coordination server.

## DERP

*Designated Encrypted Relay for Packets.* Relay servers used when two
machines can't connect directly, for example behind a strict firewall. DERP
servers only forward packets that are already end-to-end encrypted. ownscale
uses Tailscale's public DERP servers by default and can use
your own. See Tailscale's
[DERP docs](https://tailscale.com/kb/1232/derp-servers).

## Ephemeral node

A machine registered with an ephemeral auth key. It is removed automatically
once it has been offline for about two minutes. Use them for containers, CI
jobs and other short-lived workloads, so the machine list doesn't fill up
with stale entries. The browser [SSH](#tailscale-ssh) client uses one too.

## Exit node

A machine that forwards all of another machine's internet traffic, not just
tailnet traffic. Useful on untrusted Wi-Fi or to appear to browse from
another location. An exit node must be advertised by the machine
(`tailscale set --advertise-exit-node`) and approved by a manager. See
[Subnet routers and exit nodes]({{< relref "/docs/features/subnet-routers-and-exit-nodes.md" >}}).

## Funnel

In ownscale, a public HTTPS URL that forwards to a port on a machine in your
tailnet, so people outside the tailnet can reach a service. ownscale funnels
are created in the console and work differently from Tailscale's
`tailscale funnel` command, which isn't supported. See
[Funnels]({{< relref "/docs/features/funnels.md" >}}).

## Grants

A newer policy syntax (`grants`) that combines network access with
application capabilities in one rule. Grants and [ACLs](#acl) can be used
side by side. See Tailscale's [grants docs](https://tailscale.com/kb/1324/grants).

## Group

A named set of users in the policy file, such as `group:dev`, used as a
source or destination in rules.

## Headscale

The open-source implementation of the Tailscale coordination server,
maintained at [github.com/juanfont/headscale](https://github.com/juanfont/headscale).
ownscale runs one Headscale instance per organization inside its own process.

## HuJSON

*Human JSON*: JSON with comments and trailing commas. The policy file and
auth restrictions are written in HuJSON.

## Key expiry

Each machine's node key expires after a while, and the machine must sign in
again. Expiry limits the damage of a stolen device. A manager or the
machine's owner can expire a key immediately, set an expiry date or disable
expiry for servers on the machine's menu. See
Tailscale's [key expiry docs](https://tailscale.com/kb/1028/key-expiry).

## Login server

The URL a Tailscale client uses as its coordination server:
`https://<org>.<BASE_DOMAIN>`, passed with `tailscale up --login-server`. The
console shows it in the header. It is different from the console's URL
(`https://console.<BASE_DOMAIN>`) and from the [tailnet name](#tailnet-name).

## MagicDNS

Automatic DNS names for machines. With MagicDNS on, every machine can reach
another by its short name (`ssh my-server`) or full name
(`my-server.<tailnet name>`). See
[DNS]({{< relref "/docs/features/dns.md" >}}).

## Machine

A device or container running the Tailscale client and registered in a
tailnet. Tailscale calls it a *node*, and the two words mean the same thing.
Machines are listed on the **Machines** page.

## NAT traversal

The techniques Tailscale uses to open direct connections between machines
that are both behind NAT routers or firewalls. When they fail, traffic goes
through [DERP](#derp).

## Organization

ownscale's unit of tenancy. An organization is one [tailnet](#tailnet) with
its own users, machines, policy, DNS settings and Headscale instance, reached
at its own subdomain. See [Organizations]({{< relref "organizations.md" >}}).

## Policy file

The HuJSON document that holds the organization's access rules: `acls`,
`grants`, `groups`, `tagOwners`, `ssh`, `autoApprovers`, `hosts` and
`tests`. Edit it on the **Access Control** page. See
[Access control]({{< relref "/docs/features/access-control.md" >}}).

## Reusable key

An [auth key](#auth-key) that can register many machines. A key that isn't
reusable is spent after the first machine.

## Route approval

A machine can *advertise* routes (a [subnet](#subnet-router) or
[exit node](#exit-node)), but other machines don't use them until a manager
*approves* them on the machine's **Routes** dialog, or until
[auto approvers](#auto-approvers) do.

## Serve

`tailscale serve` publishes a local service to the rest of the tailnet over
HTTPS, using the machine's [certificate](#tls-certificate). It works with
ownscale when HTTPS certificates are enabled. See
[HTTPS certificates]({{< relref "/docs/features/https-certificates.md" >}}).

## Split DNS

A DNS setting that sends queries for a specific domain, such as
`corp.example.com`, to specific nameservers, while other queries use the
global nameservers. See [Split DNS]({{< relref "/docs/features/dns.md#split-dns" >}}).

## Subnet router

A machine that gives the tailnet access to a whole network that can't run
Tailscale itself, such as a LAN with printers or a cloud VPC. It advertises
the network's IP range (`--advertise-routes=192.168.1.0/24`), and a manager
approves it. See
[Subnet routers and exit nodes]({{< relref "/docs/features/subnet-routers-and-exit-nodes.md" >}}).

## Tag

A label such as `tag:server` attached to a machine. A tagged machine is owned
by its tags instead of a user, keeps working when the user who set it up
leaves, and is matched by tag in the policy file. See
[Tags]({{< relref "/docs/features/tags.md" >}}).

## Tag owners

The `tagOwners` section of the policy file says who may apply each tag.
ownscale adds a tag to `tagOwners` automatically when a manager applies it
in the console.

## Taildrive

Tailscale's file-sharing feature that exposes folders on a machine as a
WebDAV drive to other machines. It's configured through the policy file.
See [Taildrop and Taildrive]({{< relref "/docs/features/taildrop-and-taildrive.md" >}}).

## Taildrop

Sending files directly between machines in the tailnet, for example
`tailscale file cp photo.jpg my-phone:`. It's enabled for every organization.

## Tailnet

A private Tailscale network: the set of machines and users that can see each
other, along with its policy and DNS settings. In ownscale, each
[organization](#organization) is one tailnet.

## Tailnet name

The DNS suffix of the tailnet's [MagicDNS](#magicdns) names, shown on the
**DNS** page. ownscale picks a random label under `os.<CONTENT_DOMAIN>`, for
example `net3k9x2a.os.example.com`, and it can't be changed. Don't confuse it
with the [login server](#login-server).

## Tailscale SSH

SSH access to machines authorized by the policy file's `ssh` rules, with no
SSH keys to manage. Tailscale's client acts as the SSH server. The console
can also open an SSH session in the browser. See
[Tailscale SSH]({{< relref "/docs/features/tailscale-ssh.md" >}}).

## TLS certificate

A publicly trusted certificate for a machine's MagicDNS name, obtained with
`tailscale cert` or `tailscale serve`. See
[HTTPS certificates]({{< relref "/docs/features/https-certificates.md" >}}).

## WireGuard

The VPN protocol Tailscale uses to encrypt traffic between machines. It's
fast, modern and built into the Linux kernel. Tailscale handles the key
distribution that WireGuard leaves to the user.
