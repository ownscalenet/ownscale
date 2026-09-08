package tscl

import "errors"

// ErrIdentityMismatch means a valid endpoint responded with another identity.
var ErrIdentityMismatch = errors.New("endpoint identity mismatch")

// ErrInvalidResponse means an endpoint did not provide a valid identity response.
var ErrInvalidResponse = errors.New("invalid identity response")

// ErrUnreachable means the node could not establish a tailnet connection.
var ErrUnreachable = errors.New("tailnet endpoint is unreachable")

// ErrCleanup means a service or node could not complete its cleanup.
var ErrCleanup = errors.New("connectivity cleanup failed")
