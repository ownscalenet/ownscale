package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
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
func uniqueHeadscaleName(candidate, provider, userID string, existing []headscaleclient.User) string {
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
	client *headscaleclient.Client,
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
				return hu.ID, nil
			}
		}
	}

	candidate := emailLocalPart(user.Email)
	if candidate == "" {
		candidate = "user"
	}
	name := uniqueHeadscaleName(candidate, user.Provider, user.ID, hsUsers)

	created, err := client.CreateUser(ctx, headscaleclient.CreateUserParams{
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
	return created.ID, nil
}
