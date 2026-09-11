## Decision log

### 1. Start simple
Use boring technologies to keep the infra simple: no kubernetes, just a compose
file. No database HA cluster, just an sqlite file replicated continuously.

### 2. Frontend polling when creating organizations
In local development, when creating an organization, we have the option to make
the HTTP request synchronous (about 15s duration). But because the containers for
each organization live in their own network, the creation of that network triggers
an event at the OS level. Browsers listen for those events and cancel any
in-progress requests just to be sure. That's why the creation of organization
containers happens async and the frontend is polling for its status.

### 3. Users displayes as unlinked in Headplane
Headscale does not have a stand-alone login process (login is tied to linking
a machine). That's why, when first logging into Headplane, users are displayed
as not linked. They need to re-login to trigger the user matching process.

### 4. Self-signed certificates on localhost
These are created once and then mounted in all containers. This creates some
complicated code for organization provisioning process.

### 5. Headplane runs in limited mode
Because Headplane and Headscale run in different container, Headplane UX is
limited because it cannot do things like: modify headscale config and reload it.

### 6. Two-phase backup restore
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

### 7. docker default-address-pools gets exhausted
By default docker only allows 32 networks, so at max there are 32 organizations.
To increase, edit /etc/docker/daemon.json and reload docker.

### 8. TODO: remember why derper server is separate and not using headscale's embedded one

### 9. Funnels create their own subdomains
To keep routing simple, organization subdomains can only be alphanumeric. When a
funnel is created, its subdomain will be funnelname-orgname.example.com. TLS is
done with wildcards.

### 10. tsnet clients need to talk with headscale through IPs
In commit 9dbe28470, tsnet clients were switched to use IPs instead of docker
resolved DNS names. That is because the client will switch to https/443 when
a request to the controlURL(headscale) failed. That headscale instance is a 
docker container serving on hs-org_name:8080, and it has no https or 443 port.
