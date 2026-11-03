package utils

// Version is the release tag and commit this binary was built from, or "dev".
// It is set at link time with
// -ldflags "-X github.com/ownscalenet/ownscale/utils.Version=...".
var Version = "dev"
