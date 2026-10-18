package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
)

// sshKeyTTL is how long the pre-auth key minted for a browser SSH session
// stays usable. It only has to survive the wasm client's startup.
const sshKeyTTL = 3 * time.Minute

// sshSessionResponse is what the console needs to boot the in-browser
// Tailscale client and open a session to the target machine.
type sshSessionResponse struct {
	// ControlURL is the organization's headscale.
	ControlURL string `json:"controlURL"`
	// AuthKey registers the browser as an ephemeral node. It is single
	// use, short lived, and never stored by ownscale.
	AuthKey string `json:"authKey"`
	// AuthKeyID lets the console delete the key once it's been consumed,
	// instead of waiting out sshKeyTTL.
	AuthKeyID string `json:"authKeyId"`
	// Hostname is the name the browser node registers under.
	Hostname string `json:"hostname"`
	// Target is the address the SSH session connects to.
	Target string `json:"target"`
	// MachineName is the target's display name.
	MachineName string `json:"machineName"`
	// LoginName prefills the SSH username box.
	LoginName string `json:"loginName"`
}

// CreateSSHSession handles POST /api/machines/{id}/ssh-session. It mints a
// single-use ephemeral pre-auth. Whether that session is allowed is
// decided by the organization's access control policy, not here.
func (h *Handlers) CreateSSHSession(w http.ResponseWriter, r *http.Request) {
	client, org, _ := headscaleClientFromContext(r)
	id := r.PathValue("id")

	caller, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	target, err := client.GetNode(r.Context(), id)
	if err != nil && !errors.Is(err, hscl.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}
	if err != nil || funnelproxy.IsAgentUser(target.User) {
		h.writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	if target.Expired {
		h.writeError(w, http.StatusConflict, "machine's node key has expired")
		return
	}
	if !target.Online {
		h.writeError(w, http.StatusConflict, "machine is offline")
		return
	}
	addr := sshTargetAddress(target)
	if addr == "" {
		h.writeError(w, http.StatusConflict, "machine has no tailnet address")
		return
	}

	hsUser, ok := h.requireMemberHeadscaleUser(w, r, client, org, caller.ID)
	if !ok {
		return
	}

	key, err := client.CreatePreAuthKey(r.Context(), hscl.CreatePreAuthKeyParams{
		UserID:     hsUser.ID,
		Reusable:   false,
		Ephemeral:  true,
		Expiration: time.Now().Add(sshKeyTTL),
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, sshSessionResponse{
		ControlURL:  fmt.Sprintf("https://%s.%s", org.Subdomain, h.baseDomain),
		AuthKey:     key.Key,
		AuthKeyID:   key.ID,
		Hostname:    browserNodeHostname(hsUser.Name),
		Target:      addr,
		MachineName: target.GivenName,
		LoginName:   sshLoginName(hsUser),
	})
}

// sshTargetAddress picks the address the browser dials: the node's IPv4
// tailnet address if it has one, else its first address.
func sshTargetAddress(n hscl.Node) string {
	for _, ip := range n.IPAddresses {
		if addr, err := netip.ParseAddr(ip); err == nil && addr.Is4() {
			return ip
		}
	}
	if len(n.IPAddresses) > 0 {
		return n.IPAddresses[0]
	}
	return ""
}

// sshLoginName is the username the SSH box is prefilled with: the local
// part of the headscale user's email, falling back to its name.
func sshLoginName(u hscl.User) string {
	if local, _, ok := strings.Cut(u.Email, "@"); ok && local != "" {
		return sanitizeHostLabel(local)
	}
	return sanitizeHostLabel(u.Name)
}

// browserNodeHostname is the hostname the ephemeral browser node
// registers under, so it's recognizable in the machines list while it
// lasts.
func browserNodeHostname(userName string) string {
	name := sanitizeHostLabel(userName)
	if name == "" {
		return "console-ssh"
	}
	return name + "-console-ssh"
}

// sanitizeHostLabel keeps only the characters valid in a DNS label,
// lower-cased, and trims it to a sane length.
func sanitizeHostLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '_':
			b.WriteRune('-')
		}
	}
	label := strings.Trim(b.String(), "-")
	if len(label) > 30 {
		label = strings.Trim(label[:30], "-")
	}
	return label
}
