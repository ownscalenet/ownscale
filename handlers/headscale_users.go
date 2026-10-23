package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
)

// emailLocalPart returns the portion of email before "@", lowercased, or
// "" if email has no "@". Used as the starting point for a headscale
// user's Name.
func emailLocalPart(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return ""
	}
	return strings.ToLower(email[:at])
}

// shortID returns a short, filesystem/URL-safe fragment of id (an ownscale
// UUID) for use in a disambiguating suffix: the UUID's dashes are dropped
// and the first 8 characters are kept.
func shortID(id string) string {
	clean := strings.ReplaceAll(id, "-", "")
	if len(clean) > 8 {
		clean = clean[:8]
	}
	return clean
}

// uniqueHeadscaleName returns a headscale user Name derived from
// candidate that doesn't collide with any Name already in use by
// existing, trying candidate itself, then "<candidate>-<provider>" (so
// it's obvious in headscale's own user list which ownscale login produced
// which headscale identity -- headscale has no field of its own to record
// our auth provider), then "<candidate>-<provider>-<8 chars of userID>"
// as a last resort. The funnel's user name is always treated as taken.
func uniqueHeadscaleName(candidate, provider, userID string, existing []hscl.User) string {
	taken := map[string]bool{funnelproxy.AgentHeadscaleUserName: true}
	for _, u := range existing {
		taken[u.Name] = true
	}
	for _, name := range []string{
		candidate,
		fmt.Sprintf("%s-%s", candidate, provider),
		fmt.Sprintf("%s-%s-%s", candidate, provider, shortID(userID)),
	} {
		if name != "" && !taken[name] {
			return name
		}
	}
	// Every candidate collided (extremely unlikely): fall back to
	// something guaranteed unique.
	return fmt.Sprintf("%s-%s", candidate, userID)
}

// createHeadscaleUser creates the headscale user of the ownscale user
// about to be created with params, carrying the OIDC identity of params.ID
// so that person's tailnet logins land on it. It only gets the email if
// it's verified, as headscale ACL rules can name users by email. A user
// whose identity can't be set is deleted again.
func (h *Handlers) createHeadscaleUser(ctx context.Context, subdomain string, client *hscl.Client, params sqlcgen.CreateUserParams) (string, error) {
	hsUsers, err := client.ListUsers(ctx)
	if err != nil {
		return "", err
	}
	candidate := emailLocalPart(params.Email)
	if candidate == "" {
		candidate = "user"
	}
	create := hscl.CreateUserParams{
		Name:        uniqueHeadscaleName(candidate, params.Provider, params.ID, hsUsers),
		DisplayName: params.Name,
		PictureURL:  params.AvatarUrl,
	}
	if params.EmailVerified != 0 {
		create.Email = params.Email
	}
	created, err := client.CreateUser(ctx, create)
	if err != nil {
		return "", err
	}
	if err := h.orgs.SetOIDCIdentity(ctx, subdomain, created.ID, params.ID); err != nil {
		h.deleteHeadscaleUser(ctx, client, created.ID)
		return "", err
	}
	return created.ID, nil
}

// deleteHeadscaleUser deletes the headscale user id created for an ownscale
// user that then failed to be created. A failure only leaves behind a user
// nobody can log in as.
func (h *Handlers) deleteHeadscaleUser(ctx context.Context, client *hscl.Client, id string) {
	if err := client.DeleteUserAndNodes(ctx, id); err != nil {
		h.logger.Warn("delete headscale user of a failed sign-up", "headscale_user_id", id, "error", err)
	}
}

// errMemberNotFound means a member ID doesn't belong to the organization.
var errMemberNotFound = errors.New("member not found")

// memberHeadscaleUser returns the headscale user of the member memberID of org.
func (h *Handlers) memberHeadscaleUser(ctx context.Context, client *hscl.Client, org sqlcgen.Organization, memberID string) (hscl.User, error) {
	members, err := h.queries.ListUsersByOrganization(ctx, org.ID)
	if err != nil {
		return hscl.User{}, err
	}
	i := slices.IndexFunc(members, func(m sqlcgen.User) bool { return m.ID == memberID })
	if i < 0 {
		return hscl.User{}, errMemberNotFound
	}
	hsID := members[i].HeadscaleUserID
	users, err := client.ListUsers(ctx)
	if err != nil {
		return hscl.User{}, err
	}
	if j := slices.IndexFunc(users, func(u hscl.User) bool { return u.ID == hsID }); j >= 0 {
		return users[j], nil
	}
	return hscl.User{}, fmt.Errorf("headscale user %s of member %s not found", hsID, memberID)
}
