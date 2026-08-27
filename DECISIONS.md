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
