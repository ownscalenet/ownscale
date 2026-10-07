## Decision log

### 1. Start simple
Use boring technologies to keep the infra simple: no kubernetes, just a compose
file. No database HA cluster, just an sqlite file replicated continuously.

### 2. Two-phase backup restore
When restoring the databases from backup, it's ownscale's database that is restored
first, then organizations are queried (osa restore organizations) and their
databases are restored one by one. To list the existing databases, we could do as
suggested at https://litestream.io/guides/directory-watcher/#listing-available-databases
but each backup destination type (local, s3, abs, etc.) would need its own
implementation for listing databases. We rely on ownscale's database to avoid
that. Drawbacks: two-phase is more complicated than a regular list/restore.
Additionally, restoring is done manually. At startup, ownscale will
check the health of existing databases against the backup and fail when something
is off.

### 3. why derper server is separate and not using headscale's embedded one?
Because derper needs to bind on a port on the machine. We can't have all
organizations bind on the same port.

### 4. Funnels create their own subdomains
To keep routing simple, organization subdomains can only be alphanumeric. When a
funnel is created, its subdomain will be funnelname-orgname.example.com. TLS is
done with wildcards.

### 5. Funnels create a tsnet client
The client shows as a machine in the interface. A machine/node with access in
the tailnet is needed to forward the funnel to the outside world.

### 6. Headscale runs in-process, one instance per organization
Each organization used to get its own headscale docker container. Performance
tests on a 2 CPU, 2 GB VM showed that only about 10 organizations fit: each
headscale process needed only ~18 MB, but the machine stalled on IO. Now every
organization's headscale (from a fork, github.com/ownscalenet/headscale) runs
inside the ownscale process, listening on a loopback port that ownscale's
SubdomainProxy forwards `<org>.<base_domain>` traffic to. ownscale itself talks
to each instance's API over its unix socket, so no API keys are needed.
Changing an organization's settings restarts its instance.

### 7. Litestream runs in-process, not as a separate container
Backups used to run as a separate litestream container watching the data
directory. Now ownscale embeds Litestream (the `backup` package) and adds each
database when it's created, so there's no extra container and no directory
watching. This decision creates trouble because headscale is already troublesome
with the way it imports [gorm+modernc/sqlite](https://github.com/juanfont/headscale/blob/2dcd5c876ee7d37508aca52f86baefc7147ecd02/go.mod#L55).
Ownsclae adds more trouble on top because litestream comes with its own
requirements for modernc/sqlite.
Reasons why I chose to go this way:
- it simplifies the deployment steps for the user and the docker compose handling
- if backups are golang code, it's easier to test them
Reasons why it is bad:
- above trouble with the modernc import
- if backups panic, they bring the whole program down
I'm not convinced it's a good decision, but I'm willing to try it.
