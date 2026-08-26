package dockerctl_test

import (
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
)

// Static assertion that dockerfake.API satisfies dockerctl.API. It lives
// here (in an external test package) rather than in dockerfake itself, so
// dockerfake doesn't need to import dockerctl — that import would create a
// cycle with dockerctl's own (internal) test files that import dockerfake.
var _ dockerctl.API = (*dockerfake.API)(nil)
