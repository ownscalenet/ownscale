// Package hscl wraps headscale's own generated v1 API client
// (github.com/juanfont/headscale/gen/client/v1), which ships with the
// headscale module, so it always matches the server ownscale embeds.
package hscl

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	clientv1 "github.com/juanfont/headscale/gen/client/v1"

	"github.com/ownscalenet/ownscale/derrors"
)

// User is a headscale user (the owner of one or more Nodes), matching
// frontend/src/api.ts's HeadscaleUser interface field-for-field.
type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	CreatedAt   string `json:"createdAt"`
}

// Node is a headscale node/machine.
type Node struct {
	ID              string   `json:"id"`
	MachineKey      string   `json:"machineKey"`
	NodeKey         string   `json:"nodeKey"`
	IPAddresses     []string `json:"ipAddresses"`
	Name            string   `json:"name"`
	GivenName       string   `json:"givenName"`
	User            *User    `json:"user"`
	LastSeen        *string  `json:"lastSeen"`
	Expiry          *string  `json:"expiry"`
	CreatedAt       string   `json:"createdAt"`
	RegisterMethod  string   `json:"registerMethod"`
	Online          bool     `json:"online"`
	Tags            []string `json:"tags"`
	ApprovedRoutes  []string `json:"approvedRoutes"`
	AvailableRoutes []string `json:"availableRoutes"`
	SubnetRoutes    []string `json:"subnetRoutes"`
}

// PreAuthKey is a headscale pre-authentication key, matching
// frontend/src/api.ts's PreAuthKey interface field-for-field. User is nil
// for "tag only" keys (keys not tied to a user, only to ACLTags).
type PreAuthKey struct {
	ID         string   `json:"id"`
	Key        string   `json:"key"`
	User       *User    `json:"user"`
	Reusable   bool     `json:"reusable"`
	Ephemeral  bool     `json:"ephemeral"`
	Used       bool     `json:"used"`
	ACLTags    []string `json:"aclTags"`
	CreatedAt  string   `json:"createdAt"`
	Expiration string   `json:"expiration"`
}

// CreatePreAuthKeyParams are the inputs to Client.CreatePreAuthKey. UserID
// is the owning headscale user's ID, or "" for a tag-only key (ACLTags
// must be non-empty in that case).
type CreatePreAuthKeyParams struct {
	UserID     string
	Reusable   bool
	Ephemeral  bool
	Expiration time.Time
	ACLTags    []string
}

// CreateUserParams are the inputs to Client.CreateUser.
type CreateUserParams struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	PictureURL  string `json:"pictureUrl"`
}

// Client talks to a single organization's headscale management API over
// the instance's unix socket (see orgs.Service.Client).
type Client struct {
	api *clientv1.ClientWithResponses
}

// NewUnix returns a Client that talks to headscale over its local unix
// socket, which needs no API key.
func NewUnix(socketPath string) *Client {
	httpClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
	// The URL's host is ignored: every request goes over the socket.
	api, err := clientv1.NewClientWithResponses("http://local", clientv1.WithHTTPClient(httpClient))
	if err != nil {
		// Only fails for an unparsable server URL, which is a constant.
		panic(err)
	}
	return &Client{api: api}
}

// check returns an error unless resp is a 200, carrying the reason from
// headscale's application/problem+json body when there is one.
func check(resp *http.Response, problem *clientv1.ErrorModel) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var msgs []string
	if problem != nil {
		if problem.Detail != nil {
			msgs = append(msgs, *problem.Detail)
		}
		if problem.Errors != nil {
			for _, e := range *problem.Errors {
				if e.Message != nil {
					msgs = append(msgs, *e.Message)
				}
			}
		}
	}
	if len(msgs) == 0 {
		return fmt.Errorf("headscale: %s", resp.Status)
	}
	return fmt.Errorf("headscale: %s: %s", resp.Status, strings.Join(msgs, ": "))
}

// ListNodes returns every node (machine) registered on this organization's
// tailnet.
func (c *Client) ListNodes(ctx context.Context) (_ []Node, err error) {
	defer derrors.Wrap(&err, "c.ListNodes()")

	resp, err := c.api.ListNodesWithResponse(ctx, &clientv1.ListNodesParams{})
	if err != nil {
		return nil, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(resp.JSON200.Nodes))
	for _, n := range resp.JSON200.Nodes {
		nodes = append(nodes, toNode(n))
	}
	return nodes, nil
}

// ListUsers returns every user known to this organization's headscale
// instance (the set of possible node owners).
func (c *Client) ListUsers(ctx context.Context) (_ []User, err error) {
	defer derrors.Wrap(&err, "c.ListUsers()")

	resp, err := c.api.ListUsersWithResponse(ctx, &clientv1.ListUsersParams{})
	if err != nil {
		return nil, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return nil, err
	}
	users := make([]User, 0, len(resp.JSON200.Users))
	for _, u := range resp.JSON200.Users {
		users = append(users, toUser(u))
	}
	return users, nil
}

// CreateUser creates a headscale user from params.
func (c *Client) CreateUser(ctx context.Context, params CreateUserParams) (_ User, err error) {
	defer derrors.Wrap(&err, "c.CreateUser(%q)", params.Name)

	resp, err := c.api.CreateUserWithResponse(ctx, clientv1.CreateUserJSONRequestBody{
		Name:        &params.Name,
		Email:       &params.Email,
		DisplayName: &params.DisplayName,
		PictureUrl:  &params.PictureURL,
	})
	if err != nil {
		return User{}, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return User{}, err
	}
	return toUser(resp.JSON200.User), nil
}

// RenameUser renames the headscale user identified by id to newName.
func (c *Client) RenameUser(ctx context.Context, id, newName string) (err error) {
	defer derrors.Wrap(&err, "c.RenameUser(%q, %q)", id, newName)

	resp, err := c.api.RenameUserWithResponse(ctx, id, newName)
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// DeleteUser permanently removes the headscale user identified by id.
func (c *Client) DeleteUser(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteUser(%q)", id)

	resp, err := c.api.DeleteUserWithResponse(ctx, id)
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// DeleteUserAndNodes deletes the headscale user identified by id and every
// node it owns; headscale refuses to delete a user that still owns nodes.
// A user that's already gone counts as deleted, so a removal that failed
// partway can be retried.
func (c *Client) DeleteUserAndNodes(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteUserAndNodes(%q)", id)

	users, err := c.ListUsers(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(users, func(u User) bool { return u.ID == id }) {
		return nil
	}
	nodes, err := c.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if n.User != nil && n.User.ID == id {
			if err := c.DeleteNode(ctx, n.ID); err != nil {
				return err
			}
		}
	}

	return c.DeleteUser(ctx, id)
}

// RegisterNode registers a machine pending interactive authentication,
// i.e. one that ran `tailscale up --login-server=...`.
func (c *Client) RegisterNode(ctx context.Context, user, key string) (_ Node, err error) {
	defer derrors.Wrap(&err, "c.RegisterNode(%q, %q)", user, key)

	resp, err := c.api.RegisterNodeWithResponse(ctx, &clientv1.RegisterNodeParams{User: &user, Key: &key})
	if err != nil {
		return Node{}, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return Node{}, err
	}
	return toNode(resp.JSON200.Node), nil
}

// RenameNode sets nodeID's display name (givenName) to name.
func (c *Client) RenameNode(ctx context.Context, nodeID, name string) (err error) {
	defer derrors.Wrap(&err, "c.RenameNode(%q, %q)", nodeID, name)

	resp, err := c.api.RenameNodeWithResponse(ctx, nodeID, name)
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// SetTags replaces nodeID's ACL tags with tags.
func (c *Client) SetTags(ctx context.Context, nodeID string, tags []string) (err error) {
	defer derrors.Wrap(&err, "c.SetTags(%q, %v)", nodeID, tags)

	resp, err := c.api.SetTagsWithResponse(ctx, nodeID, clientv1.SetTagsJSONRequestBody{Tags: &tags})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// SetApprovedRoutes replaces the set of subnet/exit routes nodeID is
// approved to serve.
func (c *Client) SetApprovedRoutes(ctx context.Context, nodeID string, routes []string) (err error) {
	defer derrors.Wrap(&err, "c.SetApprovedRoutes(%q, %v)", nodeID, routes)

	resp, err := c.api.SetApprovedRoutesWithResponse(ctx, nodeID, clientv1.SetApprovedRoutesJSONRequestBody{Routes: &routes})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// ExpireNode immediately expires nodeID's current session, forcing it to
// re-authenticate.
func (c *Client) ExpireNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.ExpireNode(%q)", nodeID)

	resp, err := c.api.ExpireNodeWithResponse(ctx, nodeID, clientv1.ExpireNodeJSONRequestBody{})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// DeleteNode permanently removes nodeID from the tailnet.
func (c *Client) DeleteNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteNode(%q)", nodeID)

	resp, err := c.api.DeleteNodeWithResponse(ctx, nodeID)
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// ListPreAuthKeys returns every pre-authentication key for this
// organization's tailnet, across all users (and tag-only keys).
func (c *Client) ListPreAuthKeys(ctx context.Context) (_ []PreAuthKey, err error) {
	defer derrors.Wrap(&err, "c.ListPreAuthKeys()")

	resp, err := c.api.ListPreAuthKeysWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return nil, err
	}
	keys := make([]PreAuthKey, 0, len(resp.JSON200.PreAuthKeys))
	for _, k := range resp.JSON200.PreAuthKeys {
		keys = append(keys, toPreAuthKey(k))
	}
	return keys, nil
}

// CreatePreAuthKey creates a new pre-authentication key per params. The
// returned PreAuthKey.Key is the plaintext key value; headscale never
// exposes it again after this call.
func (c *Client) CreatePreAuthKey(ctx context.Context, params CreatePreAuthKeyParams) (_ PreAuthKey, err error) {
	defer derrors.Wrap(&err, "c.CreatePreAuthKey(%q)", params.UserID)

	body := clientv1.CreatePreAuthKeyJSONRequestBody{
		Reusable:  &params.Reusable,
		Ephemeral: &params.Ephemeral,
		AclTags:   &params.ACLTags,
	}
	if params.UserID != "" {
		body.User = &params.UserID
	}
	if !params.Expiration.IsZero() {
		body.Expiration = &params.Expiration
	}
	resp, err := c.api.CreatePreAuthKeyWithResponse(ctx, body)
	if err != nil {
		return PreAuthKey{}, err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return PreAuthKey{}, err
	}
	return toPreAuthKey(resp.JSON200.PreAuthKey), nil
}

// ExpirePreAuthKey immediately expires the pre-authentication key
// identified by id.
func (c *Client) ExpirePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.ExpirePreAuthKey(%q)", id)

	resp, err := c.api.ExpirePreAuthKeyWithResponse(ctx, clientv1.ExpirePreAuthKeyJSONRequestBody{Id: &id})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// DeletePreAuthKey permanently deletes the pre-authentication key identified by id.
func (c *Client) DeletePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeletePreAuthKey(%q)", id)

	resp, err := c.api.DeletePreAuthKeyWithResponse(ctx, &clientv1.DeletePreAuthKeyParams{Id: &id})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// GetPolicy returns the organization's access control policy.
func (c *Client) GetPolicy(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "c.GetPolicy()")

	resp, err := c.api.GetPolicyWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault); err != nil {
		return "", err
	}
	return resp.JSON200.Policy, nil
}

// SetPolicy replaces the organization's access control policy with
// content (HuJSON), taking effect immediately. headscale reloads its
// policy live in database mode, no restart required.
func (c *Client) SetPolicy(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "c.SetPolicy()")

	resp, err := c.api.SetPolicyWithResponse(ctx, clientv1.SetPolicyJSONRequestBody{Policy: &content})
	if err != nil {
		return err
	}
	return check(resp.HTTPResponse, resp.ApplicationproblemJSONDefault)
}

// toNode converts a generated Node into the plain Node shape handlers
// serve as JSON.
func toNode(n clientv1.Node) Node {
	node := Node{
		ID:              n.Id,
		MachineKey:      n.MachineKey,
		NodeKey:         n.NodeKey,
		IPAddresses:     n.IpAddresses,
		Name:            n.Name,
		GivenName:       n.GivenName,
		User:            toUserPtr(n.User),
		CreatedAt:       timeString(n.CreatedAt),
		RegisterMethod:  string(n.RegisterMethod),
		Online:          n.Online,
		Tags:            n.Tags,
		ApprovedRoutes:  n.ApprovedRoutes,
		AvailableRoutes: n.AvailableRoutes,
		SubnetRoutes:    n.SubnetRoutes,
	}
	if n.LastSeen != nil {
		if lastSeen := timeString(*n.LastSeen); lastSeen != "" {
			node.LastSeen = &lastSeen
		}
	}
	if n.Expiry != nil {
		if expiry := timeString(*n.Expiry); expiry != "" {
			node.Expiry = &expiry
		}
	}
	return node
}

// toUser converts a generated User into the plain User shape handlers
// serve as JSON.
func toUser(u clientv1.User) User {
	return User{
		ID:          u.Id,
		Name:        u.Name,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		CreatedAt:   timeString(u.CreatedAt),
	}
}

// toUserPtr is toUser for an optional user: headscale sends null for tagged
// nodes and tag-only keys, which the generated client decodes as a zero User.
func toUserPtr(u clientv1.User) *User {
	if u.Id == "" {
		return nil
	}
	user := toUser(u)
	return &user
}

// toPreAuthKey converts a generated PreAuthKey into the plain PreAuthKey
// shape handlers serve as JSON.
func toPreAuthKey(k clientv1.PreAuthKey) PreAuthKey {
	return PreAuthKey{
		ID:         k.Id,
		Key:        k.Key,
		User:       toUserPtr(k.User),
		Reusable:   k.Reusable,
		Ephemeral:  k.Ephemeral,
		Used:       k.Used,
		ACLTags:    k.AclTags,
		CreatedAt:  timeString(k.CreatedAt),
		Expiration: timeString(k.Expiration),
	}
}

// timeString formats t as RFC3339, or "" if it's the zero value.
func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
