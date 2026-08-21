package auth

import (
	"context"

	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
)

type contextKey int

const userContextKey contextKey = iota

// WithUser returns a copy of ctx carrying the authenticated user.
func WithUser(ctx context.Context, user *sqlcgen.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext returns the authenticated user stored on ctx, if any.
func UserFromContext(ctx context.Context) (*sqlcgen.User, bool) {
	user, ok := ctx.Value(userContextKey).(*sqlcgen.User)
	return user, ok
}
