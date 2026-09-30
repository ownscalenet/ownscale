- Make an azure blob storage and hook it up in litestream for "prod"
- Check if abs can have automatic regular snapshots/backups
- Check if litestream container logs can be changed from text to json
- dockerfile user and permissions
- requirements for the admin:
  - a name, (can't be admin?)
- TLS for DNS providers other than Azure
- when deleting a node, how to warn users of funnels that rely on the node
- add a page with explanations about huJSON for end users
- stress tests
- put inactive orgs to sleep by stopping their headscale process
- if all headscale instances run in the same process, what are error conditions
  that would bring the whole stack down (for example, does a derp map request
  failure bring down the whole process?)
