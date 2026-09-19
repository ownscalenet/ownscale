// Package headscaleclient is a small, ergonomic wrapper around the
// OpenAPI-generated headscale client in ./hscodegen (see that package's
// README-style comment for how it's regenerated). It exposes exactly the
// operations the /api/machines handlers need, translating the generated
// (protobuf-shaped) types into plain structs whose JSON tags match
// frontend/src/api.ts's Machine/HeadscaleUser shapes, so handlers can
// encode the results straight through.
package headscaleclient

import (
	"context"
	"net/http"
	"time"

	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"

	"github.com/ownscalenet/ownscale/derrors"
	hsclient "github.com/ownscalenet/ownscale/headscaleclient/hscodegen/client"
	"github.com/ownscalenet/ownscale/headscaleclient/hscodegen/client/headscale_service"
	"github.com/ownscalenet/ownscale/headscaleclient/hscodegen/models"
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

// Client talks to a single organization's headscale container's
// management API (see New and Registry, which mints and caches one Client
// per organization).
type Client struct {
	api *hsclient.Headscale
}

// authRoundTripper injects a static bearer token into every request. This
// is needed because headscale's published OpenAPI spec doesn't declare a
// security scheme (so the generated client takes no AuthInfo parameter),
// even though every management endpoint requires
// "Authorization: Bearer <api key>" in practice.
type authRoundTripper struct {
	apiKey string
	next   http.RoundTripper
}

func (rt authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+rt.apiKey)
	return rt.next.RoundTrip(req)
}

func New(host, apiKey string) *Client {
	transport := httptransport.New(host, "/", []string{"http"})
	next := transport.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	transport.Transport = authRoundTripper{apiKey: apiKey, next: next}
	return &Client{api: hsclient.New(transport, strfmt.Default)}
}

// ListNodes returns every node (machine) registered on this organization's
// tailnet.
func (c *Client) ListNodes(ctx context.Context) (_ []Node, err error) {
	defer derrors.Wrap(&err, "c.ListNodes()")

	params := headscale_service.NewHeadscaleServiceListNodesParams().WithContext(ctx)
	resp, err := c.api.HeadscaleService.HeadscaleServiceListNodes(params)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(resp.Payload.Nodes))
	for _, n := range resp.Payload.Nodes {
		nodes = append(nodes, toNode(n))
	}
	return nodes, nil
}

// ListUsers returns every user known to this organization's headscale
// instance (the set of possible node owners).
func (c *Client) ListUsers(ctx context.Context) (_ []User, err error) {
	defer derrors.Wrap(&err, "c.ListUsers()")

	params := headscale_service.NewHeadscaleServiceListUsersParams().WithContext(ctx)
	resp, err := c.api.HeadscaleService.HeadscaleServiceListUsers(params)
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(resp.Payload.Users))
	for _, u := range resp.Payload.Users {
		users = append(users, toUser(u))
	}
	return users, nil
}

type CreateUserParams struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	PictureURL  string `json:"pictureUrl"`
}

func (c *Client) CreateUser(ctx context.Context, params CreateUserParams) (_ User, err error) {
	defer derrors.Wrap(&err, "c.CreateUser(%q)", params.Name)

	p := headscale_service.NewHeadscaleServiceCreateUserParams().
		WithContext(ctx).
		WithBody(&models.V1CreateUserRequest{
			Name:        params.Name,
			Email:       params.Email,
			DisplayName: params.DisplayName,
			PictureURL:  params.PictureURL,
		})
	resp, err := c.api.HeadscaleService.HeadscaleServiceCreateUser(p)
	if err != nil {
		return User{}, err
	}
	return toUser(resp.Payload.User), nil
}

// RenameUser renames the headscale user identified by id to newName.
func (c *Client) RenameUser(ctx context.Context, id, newName string) (err error) {
	defer derrors.Wrap(&err, "c.RenameUser(%q, %q)", id, newName)

	params := headscale_service.NewHeadscaleServiceRenameUserParams().
		WithContext(ctx).
		WithOldID(id).
		WithNewName(newName)
	_, err = c.api.HeadscaleService.HeadscaleServiceRenameUser(params)
	return err
}

// DeleteUser permanently removes the headscale user identified by id.
func (c *Client) DeleteUser(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteUser(%q)", id)

	params := headscale_service.NewHeadscaleServiceDeleteUserParams().
		WithContext(ctx).
		WithID(id)
	_, err = c.api.HeadscaleService.HeadscaleServiceDeleteUser(params)
	return err
}

// RegisterNode registers a machine pending interactive authentication,
// i.e. one that ran `tailscale up --login-server=...`.
func (c *Client) RegisterNode(ctx context.Context, user, key string) (_ Node, err error) {
	defer derrors.Wrap(&err, "c.RegisterNode(%q, %q)", user, key)

	params := headscale_service.NewHeadscaleServiceRegisterNodeParams().
		WithContext(ctx).
		WithUser(&user).
		WithKey(&key)
	resp, err := c.api.HeadscaleService.HeadscaleServiceRegisterNode(params)
	if err != nil {
		return Node{}, err
	}
	return toNode(resp.Payload.Node), nil
}

// RenameNode sets nodeID's display name (givenName) to name.
func (c *Client) RenameNode(ctx context.Context, nodeID, name string) (err error) {
	defer derrors.Wrap(&err, "c.RenameNode(%q, %q)", nodeID, name)

	params := headscale_service.NewHeadscaleServiceRenameNodeParams().
		WithContext(ctx).
		WithNodeID(nodeID).
		WithNewName(name)
	_, err = c.api.HeadscaleService.HeadscaleServiceRenameNode(params)
	return err
}

// SetTags replaces nodeID's ACL tags with tags.
func (c *Client) SetTags(ctx context.Context, nodeID string, tags []string) (err error) {
	defer derrors.Wrap(&err, "c.SetTags(%q, %v)", nodeID, tags)

	params := headscale_service.NewHeadscaleServiceSetTagsParams().
		WithContext(ctx).
		WithNodeID(nodeID).
		WithBody(&models.HeadscaleServiceSetTagsBody{Tags: tags})
	_, err = c.api.HeadscaleService.HeadscaleServiceSetTags(params)
	return err
}

// SetApprovedRoutes replaces the set of subnet/exit routes nodeID is
// approved to serve.
func (c *Client) SetApprovedRoutes(ctx context.Context, nodeID string, routes []string) (err error) {
	defer derrors.Wrap(&err, "c.SetApprovedRoutes(%q, %v)", nodeID, routes)

	params := headscale_service.NewHeadscaleServiceSetApprovedRoutesParams().
		WithContext(ctx).
		WithNodeID(nodeID).
		WithBody(&models.HeadscaleServiceSetApprovedRoutesBody{Routes: routes})
	_, err = c.api.HeadscaleService.HeadscaleServiceSetApprovedRoutes(params)
	return err
}

// ExpireNode immediately expires nodeID's current session, forcing it to
// re-authenticate.
func (c *Client) ExpireNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.ExpireNode(%q)", nodeID)

	params := headscale_service.NewHeadscaleServiceExpireNodeParams().
		WithContext(ctx).
		WithNodeID(nodeID)
	_, err = c.api.HeadscaleService.HeadscaleServiceExpireNode(params)
	return err
}

// DeleteNode permanently removes nodeID from the tailnet.
func (c *Client) DeleteNode(ctx context.Context, nodeID string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteNode(%q)", nodeID)

	params := headscale_service.NewHeadscaleServiceDeleteNodeParams().
		WithContext(ctx).
		WithNodeID(nodeID)
	_, err = c.api.HeadscaleService.HeadscaleServiceDeleteNode(params)
	return err
}

// ListPreAuthKeys returns every pre-authentication key for this
// organization's tailnet, across all users (and tag-only keys).
func (c *Client) ListPreAuthKeys(ctx context.Context) (_ []PreAuthKey, err error) {
	defer derrors.Wrap(&err, "c.ListPreAuthKeys()")

	params := headscale_service.NewHeadscaleServiceListPreAuthKeysParams().WithContext(ctx)
	resp, err := c.api.HeadscaleService.HeadscaleServiceListPreAuthKeys(params)
	if err != nil {
		return nil, err
	}
	keys := make([]PreAuthKey, 0, len(resp.Payload.PreAuthKeys))
	for _, k := range resp.Payload.PreAuthKeys {
		keys = append(keys, toPreAuthKey(k))
	}
	return keys, nil
}

// CreatePreAuthKey creates a new pre-authentication key per params. The
// returned PreAuthKey.Key is the plaintext key value; headscale never
// exposes it again after this call.
func (c *Client) CreatePreAuthKey(ctx context.Context, params CreatePreAuthKeyParams) (_ PreAuthKey, err error) {
	defer derrors.Wrap(&err, "c.CreatePreAuthKey(%q)", params.UserID)

	req := &models.V1CreatePreAuthKeyRequest{
		User:      params.UserID,
		Reusable:  params.Reusable,
		Ephemeral: params.Ephemeral,
		ACLTags:   params.ACLTags,
	}
	if !params.Expiration.IsZero() {
		req.Expiration = strfmt.DateTime(params.Expiration)
	}
	p := headscale_service.NewHeadscaleServiceCreatePreAuthKeyParams().
		WithContext(ctx).
		WithBody(req)
	resp, err := c.api.HeadscaleService.HeadscaleServiceCreatePreAuthKey(p)
	if err != nil {
		return PreAuthKey{}, err
	}
	return toPreAuthKey(resp.Payload.PreAuthKey), nil
}

// ExpirePreAuthKey immediately expires the pre-authentication key
// identified by id.
func (c *Client) ExpirePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.ExpirePreAuthKey(%q)", id)

	params := headscale_service.NewHeadscaleServiceExpirePreAuthKeyParams().
		WithContext(ctx).
		WithBody(&models.V1ExpirePreAuthKeyRequest{ID: id})
	_, err = c.api.HeadscaleService.HeadscaleServiceExpirePreAuthKey(params)
	return err
}

// DeletePreAuthKey permanently deletes the pre-authentication key identified by id.
func (c *Client) DeletePreAuthKey(ctx context.Context, id string) (err error) {
	defer derrors.Wrap(&err, "c.DeletePreAuthKey(%q)", id)

	params := headscale_service.NewHeadscaleServiceDeletePreAuthKeyParams().
		WithContext(ctx).
		WithID(&id)
	_, err = c.api.HeadscaleService.HeadscaleServiceDeletePreAuthKey(params)
	return err
}

// GetPolicy returns the organization's access control policy.
func (c *Client) GetPolicy(ctx context.Context) (_ string, err error) {
	defer derrors.Wrap(&err, "c.GetPolicy()")

	params := headscale_service.NewHeadscaleServiceGetPolicyParams().WithContext(ctx)
	resp, err := c.api.HeadscaleService.HeadscaleServiceGetPolicy(params)
	if err != nil {
		return "", err
	}
	return resp.Payload.Policy, nil
}

// SetPolicy replaces the organization's access control policy with
// content (HuJSON), taking effect immediately. headscale reloads its
// policy live in database mode, no restart required.
func (c *Client) SetPolicy(ctx context.Context, content string) (err error) {
	defer derrors.Wrap(&err, "c.SetPolicy()")

	params := headscale_service.NewHeadscaleServiceSetPolicyParams().
		WithContext(ctx).
		WithBody(&models.V1SetPolicyRequest{Policy: content})
	_, err = c.api.HeadscaleService.HeadscaleServiceSetPolicy(params)
	return err
}

// toNode converts a generated V1Node into the plain Node shape handlers
// serve as JSON.
func toNode(n *models.V1Node) Node {
	if n == nil {
		return Node{}
	}
	node := Node{
		ID:              n.ID,
		MachineKey:      n.MachineKey,
		NodeKey:         n.NodeKey,
		IPAddresses:     n.IPAddresses,
		Name:            n.Name,
		GivenName:       n.GivenName,
		CreatedAt:       dateTimeString(n.CreatedAt),
		Online:          n.Online,
		Tags:            n.Tags,
		ApprovedRoutes:  n.ApprovedRoutes,
		AvailableRoutes: n.AvailableRoutes,
		SubnetRoutes:    n.SubnetRoutes,
	}
	if n.RegisterMethod != nil {
		node.RegisterMethod = string(*n.RegisterMethod)
	}
	if n.User != nil {
		u := toUser(n.User)
		node.User = &u
	}
	if lastSeen := dateTimeString(n.LastSeen); lastSeen != "" {
		node.LastSeen = &lastSeen
	}
	if expiry := dateTimeString(n.Expiry); expiry != "" {
		node.Expiry = &expiry
	}
	return node
}

// toUser converts a generated V1User into the plain User shape handlers
// serve as JSON.
func toUser(u *models.V1User) User {
	if u == nil {
		return User{}
	}
	return User{
		ID:          u.ID,
		Name:        u.Name,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		CreatedAt:   dateTimeString(u.CreatedAt),
	}
}

// toPreAuthKey converts a generated V1PreAuthKey into the plain
// PreAuthKey shape handlers serve as JSON.
func toPreAuthKey(k *models.V1PreAuthKey) PreAuthKey {
	if k == nil {
		return PreAuthKey{}
	}
	key := PreAuthKey{
		ID:         k.ID,
		Key:        k.Key,
		Reusable:   k.Reusable,
		Ephemeral:  k.Ephemeral,
		Used:       k.Used,
		ACLTags:    k.ACLTags,
		CreatedAt:  dateTimeString(k.CreatedAt),
		Expiration: dateTimeString(k.Expiration),
	}
	if k.User != nil {
		u := toUser(k.User)
		key.User = &u
	}
	return key
}

// dateTimeString formats dt as RFC3339, or "" if it's the zero value.
func dateTimeString(dt strfmt.DateTime) string {
	t := time.Time(dt)
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
