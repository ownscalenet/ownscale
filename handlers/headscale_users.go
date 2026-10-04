package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
)

// claimedHeadscaleUserIDs returns the set of headscale user IDs already
// linked to members of the organization (used to keep two ownscale users
// that share an email address -- e.g. a Google account and a Microsoft
// account provisioned for the same person -- from fighting over, or
// silently merging into, the same headscale user; see ensureHeadscaleUser).
func claimedHeadscaleUserIDs(members []sqlcgen.User) map[string]bool {
	claimed := make(map[string]bool, len(members))
	for _, m := range members {
		if m.HeadscaleUserID.Valid {
			claimed[m.HeadscaleUserID.String] = true
		}
	}
	return claimed
}

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
// as a last resort.
func uniqueHeadscaleName(candidate, provider, userID string, existing []hscl.User) string {
	taken := make(map[string]bool, len(existing))
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

func (h *Handlers) ensureHeadscaleUser(
	ctx context.Context,
	subdomain string,
	client *hscl.Client,
	user sqlcgen.User,
	claimed map[string]bool) (_ string, err error) {
	if user.HeadscaleUserID.Valid {
		return user.HeadscaleUserID.String, nil
	}

	hsUsers, err := client.ListUsers(ctx)
	if err != nil {
		return "", err
	}

	if user.Email != "" {
		for _, hu := range hsUsers {
			if claimed[hu.ID] {
				continue
			}
			if strings.EqualFold(hu.Email, user.Email) {
				if err := h.queries.LinkUserHeadscaleUser(ctx, sqlcgen.LinkUserHeadscaleUserParams{
					HeadscaleUserID: sql.NullString{String: hu.ID, Valid: true},
					ID:              user.ID,
				}); err != nil {
					return "", err
				}
				h.setOIDCIdentity(ctx, subdomain, hu.ID, user.ID)
				return hu.ID, nil
			}
		}
	}

	candidate := emailLocalPart(user.Email)
	if candidate == "" {
		candidate = "user"
	}
	name := uniqueHeadscaleName(candidate, user.Provider, user.ID, hsUsers)

	created, err := client.CreateUser(ctx, hscl.CreateUserParams{
		Name:        name,
		Email:       user.Email,
		DisplayName: user.Name,
		PictureURL:  user.AvatarUrl,
	})
	if err != nil {
		return "", err
	}

	if err := h.queries.LinkUserHeadscaleUser(ctx, sqlcgen.LinkUserHeadscaleUserParams{
		HeadscaleUserID: sql.NullString{String: created.ID, Valid: true},
		ID:              user.ID,
	}); err != nil {
		return "", err
	}
	h.setOIDCIdentity(ctx, subdomain, created.ID, user.ID)
	return created.ID, nil
}

// linkMembers links each of members, the members of subdomain's
// organization, to a headscale user if it has none, updating members in
// place. It's best-effort: a member that fails to link, e.g. as headscale
// rejected the create call or another request linked it first, stays
// unlinked.
func (h *Handlers) linkMembers(ctx context.Context, subdomain string, client *hscl.Client, members []sqlcgen.User) {
	claimed := claimedHeadscaleUserIDs(members)
	for i, m := range members {
		if m.HeadscaleUserID.Valid {
			continue
		}
		hsID, err := h.ensureHeadscaleUser(ctx, subdomain, client, m, claimed)
		if err != nil {
			h.logger.Warn("link member to headscale user", "subdomain", subdomain, "user_id", m.ID, "error", err)
			continue
		}
		members[i].HeadscaleUserID = sql.NullString{String: hsID, Valid: true}
		claimed[hsID] = true
	}
}

// linkAllMembers links every member of subdomain's organization to a
// headscale user. It runs when the organization's headscale starts (see
// orgs.Service.OnStart), so a member's first OIDC login through headscale
// finds their headscale user instead of creating another one.
func (h *Handlers) linkAllMembers(ctx context.Context, subdomain string, client *hscl.Client) {
	org, err := h.queries.GetOrganizationBySubdomain(ctx, subdomain)
	if err != nil {
		h.logger.Warn("link members on start: get organization", "subdomain", subdomain, "error", err)
		return
	}
	members, err := h.queries.ListUsersByOrganization(ctx, org.ID)
	if err != nil {
		h.logger.Warn("link members on start: list members", "subdomain", subdomain, "error", err)
		return
	}
	h.linkMembers(ctx, subdomain, client, members)
}

// errMemberNotFound means a member ID doesn't belong to the organization.
var errMemberNotFound = errors.New("member not found")

// memberHeadscaleUser returns the headscale user of the member memberID of
// org, linking one first if the member has none (see ensureHeadscaleUser).
func (h *Handlers) memberHeadscaleUser(ctx context.Context, client *hscl.Client, org sqlcgen.Organization, memberID string) (hscl.User, error) {
	members, err := h.queries.ListUsersByOrganization(ctx, org.ID)
	if err != nil {
		return hscl.User{}, err
	}
	i := slices.IndexFunc(members, func(m sqlcgen.User) bool { return m.ID == memberID })
	if i < 0 {
		return hscl.User{}, errMemberNotFound
	}
	hsID, err := h.ensureHeadscaleUser(ctx, org.Subdomain, client, members[i], claimedHeadscaleUserIDs(members))
	if err != nil {
		return hscl.User{}, err
	}
	users, err := client.ListUsers(ctx)
	if err != nil {
		return hscl.User{}, err
	}
	if j := slices.IndexFunc(users, func(u hscl.User) bool { return u.ID == hsID }); j >= 0 {
		return users[j], nil
	}
	return hscl.User{}, fmt.Errorf("headscale user %s of member %s not found", hsID, memberID)
}

// setOIDCIdentity is orgs.Service.SetOIDCIdentity, best-effort: a failure
// only risks a duplicate headscale user on the person's first OIDC login.
func (h *Handlers) setOIDCIdentity(ctx context.Context, subdomain, headscaleUserID, userID string) {
	if err := h.orgs.SetOIDCIdentity(ctx, subdomain, headscaleUserID, userID); err != nil {
		h.logger.Warn("set headscale user OIDC identity", "subdomain", subdomain, "user_id", userID, "error", err)
	}
}
