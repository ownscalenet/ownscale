// Package hscl wraps headscale's own generated v1 API client
// (github.com/juanfont/headscale/gen/client/v1), which ships with the
// headscale module, so it always matches the server ownscale embeds.
package hscl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	clientv1 "github.com/juanfont/headscale/gen/client/v1"
	clientv2 "github.com/juanfont/headscale/gen/client/v2"

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
	// ProviderID is the user's OIDC identity, empty for users created via the API.
	ProviderID string `json:"-"`
}

// Node is a headscale node/machine.
type Node struct {
	ID          string   `json:"id"`
	MachineKey  string   `json:"machineKey"`
	NodeKey     string   `json:"nodeKey"`
	IPAddresses []string `json:"ipAddresses"`
	Name        string   `json:"name"`
	GivenName   string   `json:"givenName"`
	User        *User    `json:"user"`
	LastSeen    *string  `json:"lastSeen"`
	Expiry      *string  `json:"expiry"`
	// Expired reports whether Expiry has passed by this server's clock, so
	// clients don't compare it with their own, possibly skewed, clock.
	Expired         bool     `json:"expired"`
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
	// Expired reports whether Expiration has passed by this server's clock,
	// so clients don't compare it with their own, possibly skewed, clock.
	Expired bool `json:"expired"`
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
	// apiV2 serves what v1 lacks, such as the reported host info.
	apiV2 *clientv2.ClientWithResponses
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
	apiV2, err := clientv2.NewClientWithResponses("http://local", clientv2.WithHTTPClient(httpClient))
	if err != nil {
		panic(err)
	}
	return &Client{api: api, apiV2: apiV2}
}

// ErrNotFound is returned by lookups for an object this organization's
// headscale instance doesn't have.
var ErrNotFound = errors.New("not found")

// response is what every generated XxxWithResponse call returns, with T the
// operation's 200 body.
type response[T any] interface {
	StatusCode() int
	Status() string
	GetJSON200() *T
	GetApplicationproblemJSONDefault() *clientv1.ErrorModel
}

// result returns resp's 200 JSON body, or an error carrying the reason from
// headscale's application/problem+json body when there is one. A 200 without
// a JSON body is an error too, so callers never see a nil body.
func result[T any, R response[T]](resp R, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if body := resp.GetJSON200(); resp.StatusCode() == http.StatusOK && body != nil {
		return body, nil
	}
	var msgs []string
	if problem := resp.GetApplicationproblemJSONDefault(); problem != nil {
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
	if resp.StatusCode() == http.StatusOK {
		msgs = append(msgs, "response has no JSON body")
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("headscale: %s", resp.Status())
	}
	return nil, fmt.Errorf("headscale: %s: %s", resp.Status(), strings.Join(msgs, ": "))
}

// convert applies f to each element of in.
func convert[A, B any](in []A, f func(A) B) []B {
	out := make([]B, 0, len(in))
	for _, a := range in {
		out = append(out, f(a))
	}
	return out
}

// ListNodes returns every node (machine) registered on this organization's
// tailnet.
func (c *Client) ListNodes(ctx context.Context) (_ []Node, err error) {
	defer derrors.Wrap(&err, "c.ListNodes()")

	out, err := result(c.api.ListNodesWithResponse(ctx, &clientv1.ListNodesParams{}))
	if err != nil {
		return nil, err
	}
	return convert(out.Nodes, toNode), nil
}

// GetNode returns the node with nodeID, or ErrNotFound if this
// organization's tailnet has no such node.
func (c *Client) GetNode(ctx context.Context, nodeID string) (_ Node, err error) {
	defer derrors.Wrap(&err, "c.GetNode(%q)", nodeID)

	resp, err := c.api.GetNodeWithResponse(ctx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if resp.StatusCode() == http.StatusNotFound {
		return Node{}, ErrNotFound
	}
	out, err := result[clientv1.NodeOutputBody](resp, nil)
	if err != nil {
		return Node{}, err
	}
	return toNode(out.Node), nil
}

// ListUsers returns every user known to this organization's headscale
// instance (the set of possible node owners).
func (c *Client) ListUsers(ctx context.Context) (_ []User, err error) {
	defer derrors.Wrap(&err, "c.ListUsers()")

	out, err := result(c.api.ListUsersWithResponse(ctx, &clientv1.ListUsersParams{}))
	if err != nil {
		return nil, err
	}
	return convert(out.Users, toUser), nil
}

// CreateUser creates a headscale user from params.
func (c *Client) CreateUser(ctx context.Context, params CreateUserParams) (_ User, err error) {
	defer derrors.Wrap(&err, "c.CreateUser(%q)", params.Name)

	out, err := result(c.api.CreateUserWithResponse(ctx, clientv1.CreateUserJSONRequestBody{
		Name:        &params.Name,
		Email:       &params.Email,
		DisplayName: &params.DisplayName,
		PictureUrl:  &params.PictureURL,
	}))
	if err != nil {
		return User{}, err
	}
	return toUser(out.User), nil
}

// RenameUser renames the headscale user identified by id to newName.
func (c *Client) RenameUser(ctx context.Context, id, newName string) (err error) {
	defer derrors.Wrap(&err, "c.RenameUser(%q, %q)", id, newName)

	_, err = result(c.api.RenameUserWithResponse(ctx, id, newName))
	return err
}

// DeleteUser permanently removes the headscale user identified by id.
func (c *Client) DeleteUser(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteUser(%q)", id)

	_, err = result(c.api.DeleteUserWithResponse(ctx, id))
	return err
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

	out, err := result(c.api.RegisterNodeWithResponse(ctx, &clientv1.RegisterNodeParams{User: &user, Key: &key}))
	if err != nil {
		return Node{}, err
	}
	return toNode(out.Node), nil
}

// RenameNode sets nodeID's display name (givenName) to name.
func (c *Client) RenameNode(ctx context.Context, nodeID, name string) (err error) {
	defer derrors.Wrap(&err, "c.RenameNode(%q, %q)", nodeID, name)

	_, err = result(c.api.RenameNodeWithResponse(ctx, nodeID, name))
	return err
}

// SetTags replaces nodeID's ACL tags with tags.
func (c *Client) SetTags(ctx context.Context, nodeID string, tags []string) (err error) {
	defer derrors.Wrap(&err, "c.SetTags(%q, %v)", nodeID, tags)

	_, err = result(c.api.SetTagsWithResponse(ctx, nodeID, clientv1.SetTagsJSONRequestBody{Tags: &tags}))
	return err
}

// SetApprovedRoutes replaces the set of subnet/exit routes nodeID is
// approved to serve.
func (c *Client) SetApprovedRoutes(ctx context.Context, nodeID string, routes []string) (err error) {
	defer derrors.Wrap(&err, "c.SetApprovedRoutes(%q, %v)", nodeID, routes)

	_, err = result(c.api.SetApprovedRoutesWithResponse(ctx, nodeID, clientv1.SetApprovedRoutesJSONRequestBody{Routes: &routes}))
	return err
}

// ExpireNode immediately expires nodeID's current session, forcing it to
// re-authenticate.
func (c *Client) ExpireNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.ExpireNode(%q)", nodeID)

	_, err = result(c.api.ExpireNodeWithResponse(ctx, nodeID, clientv1.ExpireNodeJSONRequestBody{}))
	return err
}

// SetNodeExpiry sets nodeID's key expiry to at, re-enabling expiry for a node
// that never expired. Headscale's v2 API can only disable expiry, so this
// uses v1.
func (c *Client) SetNodeExpiry(ctx context.Context, nodeID string, at time.Time) (err error) {
	defer derrors.Wrap(&err, "c.SetNodeExpiry(%q, %s)", nodeID, at.Format(time.RFC3339))

	_, err = result(c.api.ExpireNodeWithResponse(ctx, nodeID, clientv1.ExpireNodeJSONRequestBody{Expiry: &at}))
	return err
}

// DisableNodeExpiry makes nodeID's key never expire, or returns ErrNotFound
// if there is no such node.
func (c *Client) DisableNodeExpiry(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.DisableNodeExpiry(%q)", nodeID)

	resp, err := c.apiV2.SetDeviceKeyWithResponse(ctx, nodeID, clientv2.SetDeviceKeyJSONRequestBody{KeyExpiryDisabled: true})
	if err != nil {
		return err
	}
	if resp.StatusCode() == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("headscale: %s", resp.Status())
	}
	return nil
}

// DeleteNode permanently removes nodeID from the tailnet.
func (c *Client) DeleteNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteNode(%q)", nodeID)

	_, err = result(c.api.DeleteNodeWithResponse(ctx, nodeID))
	return err
}

// ListPreAuthKeys returns every pre-authentication key for this
// organization's tailnet, across all users (and tag-only keys).
func (c *Client) ListPreAuthKeys(ctx context.Context) (_ []PreAuthKey, err error) {
	defer derrors.Wrap(&err, "c.ListPreAuthKeys()")

	out, err := result(c.api.ListPreAuthKeysWithResponse(ctx))
	if err != nil {
		return nil, err
	}
	return convert(out.PreAuthKeys, toPreAuthKey), nil
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
	out, err := result(c.api.CreatePreAuthKeyWithResponse(ctx, body))
	if err != nil {
		return PreAuthKey{}, err
	}
	return toPreAuthKey(out.PreAuthKey), nil
}

// ExpirePreAuthKey immediately expires the pre-authentication key
// identified by id.
func (c *Client) ExpirePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.ExpirePreAuthKey(%q)", id)

	_, err = result(c.api.ExpirePreAuthKeyWithResponse(ctx, clientv1.ExpirePreAuthKeyJSONRequestBody{Id: &id}))
	return err
}

// DeletePreAuthKey permanently deletes the pre-authentication key identified by id.
func (c *Client) DeletePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeletePreAuthKey(%q)", id)

	_, err = result(c.api.DeletePreAuthKeyWithResponse(ctx, &clientv1.DeletePreAuthKeyParams{Id: &id}))
	return err
}

// GetPolicy returns the organization's access control policy.
func (c *Client) GetPolicy(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "c.GetPolicy()")

	out, err := result(c.api.GetPolicyWithResponse(ctx))
	if err != nil {
		return "", err
	}
	return out.Policy, nil
}

// PolicyError is headscale's reason for rejecting a policy, such as a syntax
// problem or the failing entries of its "tests" block (one per line).
type PolicyError struct {
	Reason string
}

func (e *PolicyError) Error() string { return e.Reason }

// policyError returns a *PolicyError when headscale rejected the policy
// itself (400), and otherwise the generic error from result.
func policyError(status int, problem *clientv1.ErrorModel, err error) error {
	if status != http.StatusBadRequest || problem == nil || problem.Detail == nil {
		return err
	}
	// The errors carry the specifics (such as each failing test); detail is
	// only a generic summary like "setting policy".
	var msgs []string
	if problem.Errors != nil {
		for _, e := range *problem.Errors {
			if e.Message != nil {
				msgs = append(msgs, *e.Message)
			}
		}
	}
	reason := *problem.Detail
	if len(msgs) > 0 {
		reason = strings.Join(msgs, "\n")
	}
	return &PolicyError{Reason: reason}
}

// SetPolicy replaces the organization's access control policy with
// content (HuJSON), taking effect immediately. headscale reloads its
// policy live in database mode, no restart required. It returns a
// *PolicyError if headscale rejects the policy, including when the policy's
// "tests" fail.
func (c *Client) SetPolicy(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "c.SetPolicy()")

	resp, err := c.api.SetPolicyWithResponse(ctx, clientv1.SetPolicyJSONRequestBody{Policy: &content})
	_, err = result(resp, err)
	if resp != nil {
		err = policyError(resp.StatusCode(), resp.ApplicationproblemJSONDefault, err)
	}
	return err
}

// CheckPolicy validates content against the organization's live users and
// machines, running the policy's "tests", without saving it. It returns a
// *PolicyError if headscale rejects the policy.
func (c *Client) CheckPolicy(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "c.CheckPolicy()")

	resp, err := c.api.CheckPolicyWithResponse(ctx, clientv1.CheckPolicyJSONRequestBody{Policy: &content})
	_, err = result(resp, err)
	if resp != nil {
		err = policyError(resp.StatusCode(), resp.ApplicationproblemJSONDefault, err)
	}
	return err
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
		node.Expired = expired(*n.Expiry)
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
		ProviderID:  u.ProviderId,
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
		Expired:    expired(k.Expiration),
	}
}

// expired reports whether t is set and not in the future. Expiring
// something sets its expiry to the current time, so equal counts.
func expired(t time.Time) bool {
	return !t.IsZero() && !t.After(time.Now())
}

// timeString formats t as RFC3339, or "" if it's the zero value.
func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// DeviceInfo is what a node reported about its host. Fields are empty until
// the node has reported them.
type DeviceInfo struct {
	OS              string `json:"os"`
	ClientVersion   string `json:"clientVersion"`
	Hostname        string `json:"hostname"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// GetDeviceInfo returns the host info reported by node nodeID, or
// ErrNotFound if this organization's tailnet has no such node.
func (c *Client) GetDeviceInfo(ctx context.Context, nodeID string) (_ DeviceInfo, err error) {
	defer derrors.Wrap(&err, "c.GetDeviceInfo(%q)", nodeID)

	resp, err := c.apiV2.GetDeviceWithResponse(ctx, nodeID, &clientv2.GetDeviceParams{})
	if err != nil {
		return DeviceInfo{}, err
	}
	if resp.StatusCode() == http.StatusNotFound {
		return DeviceInfo{}, ErrNotFound
	}
	if resp.JSON200 == nil {
		return DeviceInfo{}, fmt.Errorf("headscale: %s", resp.Status())
	}
	d := resp.JSON200
	return DeviceInfo{OS: d.Os, ClientVersion: d.ClientVersion, Hostname: d.Hostname, UpdateAvailable: d.UpdateAvailable}, nil
}
