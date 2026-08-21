- All organizations are on the same docker network.
Each should have their own network to avoid cross-org talk.
- Make an azure blob storage and hook in up in litestream for "prod"
- Check if abs can have automatic regular snapshots/backups
- Hook up headplane auth with our own OIDC auth (right now, to use the headplane
frontend, one needs to get a headscale key from the headscale container)
- Set up a container for a derp server and have it loaded for each organization.
- Check if litestream container logs can be changed from text to json

