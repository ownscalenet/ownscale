package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
	"github.com/ownscalenet/ownscale/orgs"
)

// fakeHeadscaleClients stands in for orgs.Service's running headscale
// instances, returning a client for a fake headscale API.
type fakeHeadscaleClients struct {
	client *hscl.Client
	err    error
}

func (f fakeHeadscaleClients) Wake(context.Context, string) (*hscl.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func (f fakeHeadscaleClients) Ensure(_ context.Context, subdomain string) (*hscl.Client, error) {
	return f.Wake(context.Background(), subdomain)
}

// lazyHeadscaleClients is fakeHeadscaleClients for an organization whose
// headscale hasn't started yet: Wake reports it unprovisioned until
// Ensure starts it.
type lazyHeadscaleClients struct {
	client  *hscl.Client
	started bool
}

func (l *lazyHeadscaleClients) Wake(context.Context, string) (*hscl.Client, error) {
	if !l.started {
		return nil, orgs.ErrNotProvisioned
	}
	return l.client, nil
}

func (l *lazyHeadscaleClients) Ensure(context.Context, string) (*hscl.Client, error) {
	l.started = true
	return l.client, nil
}

// newFakeHeadscaleServer builds a fake implementing enough of
// headscale's management API to exercise the /api/machines handlers, and
// a *hscl.Client pointed at it. The returned *string is the
// in-memory ACL policy GetPolicy/SetPolicy serve/mutate, starting out with
// no "tagOwners" section (matching a freshly provisioned org).
func newFakeHeadscaleServer(t *testing.T) (*hscl.Client, *[]*http.Request, *string, *[]string) {
	t.Helper()
	var requests []*http.Request
	policy := `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`
	var lastTags []string

	nodes := map[string]string{
		"1": `{"id":"1","name":"node1","givenName":"node1","ipAddresses":["100.64.0.1"],
			 "user":{"id":"1","name":"alice"},"online":true,"tags":["tag:server"],
			 "approvedRoutes":["10.0.0.0/24"],"availableRoutes":["10.0.0.0/24"],
			 "createdAt":"2024-01-01T00:00:00Z"}`,
		"2": fmt.Sprintf(`{"id":"2","name":"funnel-acme","givenName":"funnel-acme","ipAddresses":["100.64.0.2"],
			 "user":{"id":"2","name":%q},"online":false,
			 "expiry":"2020-01-01T00:00:00Z","createdAt":"2024-01-01T00:00:00Z"}`, funnelproxy.FunnelHeadscaleUserName),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"nodes":[%s,%s]}`, nodes["1"], nodes["2"])
	})
	mux.HandleFunc("/api/v2/device/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"1","os":"linux","clientVersion":"1.80.0","hostname":"node1-host"}`)
	})
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"user":{"id":"3","name":"new-user"}}`)
			return
		}
		fmt.Fprint(w, `{"users":[{"id":"1","name":"alice","email":"alice@example.com"},{"id":"2","name":"bob","email":"bob@example.com"}]}`)
	})
	mux.HandleFunc("/api/v1/user/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/user/1", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/user/3", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/tags", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Tags []string `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastTags = body.Tags
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/approve_routes", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v2/device/1/key", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/1/expire", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/{id}", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			fmt.Fprint(w, `{}`)
			return
		}
		node, ok := nodes[r.PathValue("id")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"detail":"node not found"}`)
			return
		}
		fmt.Fprintf(w, `{"node":%s}`, node)
	})
	mux.HandleFunc("/api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body struct {
				Policy string `json:"policy"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			policy = body.Policy
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprintf(w, `{"policy":%q}`, policy)
	})
	mux.HandleFunc("/api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"preAuthKey":{
				"id":"2","key":"nodekey:newkey","user":{"id":"1","name":"alice"},
				"reusable":true,"ephemeral":false,"used":false,"aclTags":["tag:server"],
				"createdAt":"2024-03-01T00:00:00Z","expiration":"2024-06-01T00:00:00Z"
			}}`)
			return
		}
		fmt.Fprint(w, `{"preAuthKeys":[
			{"id":"1","key":"nodekey:existing","user":{"id":"1","name":"alice"},
			 "reusable":false,"ephemeral":false,"used":false,"aclTags":[],
			 "createdAt":"2024-01-01T00:00:00Z","expiration":"2024-06-01T00:00:00Z"}
		]}`)
	})
	mux.HandleFunc("/api/v1/preauthkey/expire", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("key") == "hskey-authreq-failme00000000000000000000" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"registration key not found"}`)
			return
		}
		fmt.Fprintf(w, `{"node":{"id":"4","name":"new-machine","givenName":"new-machine","user":{"id":"1","name":%q}}}`, r.URL.Query().Get("user"))
	})

	client := hscl.NewUnix(hstest.Socket(t, mux))
	return client, &requests, &policy, &lastTags
}

// activeOrgUser inserts the owner of a new active organization and points
// h.hs at a fake headscale (used by handlers that call
// resolveHeadscaleClient directly, e.g. Session/ListMembers's best-effort
// linking). Returns the user, a client for the middleware-based handlers
// to attach via withHeadscaleClient, and the recorded requests slice.
func activeOrgUser(t *testing.T, h *Handlers, queries *dbpkg.TxQueries) (sqlcgen.User, *hscl.Client, *[]*http.Request) {
	t.Helper()
	user, client, requests, _, _ := activeOrgUserWithPolicy(t, h, queries)
	return user, client, requests
}

// activeOrgUserWithPolicy is activeOrgUser plus a pointer to the fake
// server's in-memory ACL policy and to the tags from the most recent
// .../tags request, for tests exercising SetMachineTags's normalization
// and tagOwners auto-provisioning.
func activeOrgUserWithPolicy(t *testing.T, h *Handlers, queries *dbpkg.TxQueries) (sqlcgen.User, *hscl.Client, *[]*http.Request, *string, *[]string) {
	t.Helper()
	user := insertUser(t, queries)

	client, requests, policy, lastTags := newFakeHeadscaleServer(t)
	h.hs = fakeHeadscaleClients{client: client}

	return user, client, requests, policy, lastTags
}

// TestAssignableUsers checks which members ListMachines and ListAuthKeys
// offer for registering machines and creating keys, with or without a
// running headscale.
func TestAssignableUsers(t *testing.T) {
	endpoints := []struct {
		name    string
		handler func(*Handlers) http.HandlerFunc
	}{
		{"machines", func(h *Handlers) http.HandlerFunc { return h.ListMachines }},
		{"auth keys", func(h *Handlers) http.HandlerFunc { return h.ListAuthKeys }},
	}
	for _, ep := range endpoints {
		for _, provisioned := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/provisioned=%v", ep.name, provisioned), func(t *testing.T) {
				h, org, users := newOrgTestHandlers(t, RoleOwner, RoleMember)
				owner, member := users[0], users[1]
				var client *hscl.Client
				if provisioned {
					client, _, _, _ = newFakeHeadscaleServer(t)
				}

				for _, tc := range []struct {
					caller sqlcgen.User
					want   []string
				}{
					{owner, []string{owner.ID, member.ID}},
					{member, []string{member.ID}},
				} {
					req := withTestUser(httptest.NewRequest(http.MethodGet, "/", nil), tc.caller)
					rec := httptest.NewRecorder()
					ep.handler(h)(rec, withHeadscaleContext(req, client, org))

					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
					}
					var body struct {
						AssignableUsers []assignableUser `json:"assignableUsers"`
					}
					if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
						t.Fatalf("decode: %v", err)
					}
					var got []string
					for _, u := range body.AssignableUsers {
						got = append(got, u.ID)
					}
					if !slices.Equal(got, tc.want) {
						t.Errorf("%s: assignableUsers = %v, want %v", tc.caller.Role, got, tc.want)
					}
				}
			})
		}
	}
}

func TestListMachinesReturnsNodesAndUsers(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user), client)
	rec := httptest.NewRecorder()
	h.ListMachines(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Machines []machineResponse `json:"machines"`
		Users    []hscl.User       `json:"users"`
		Total    int               `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 || len(body.Machines) != 2 {
		t.Errorf("got %d/%d machines, want 2/2", len(body.Machines), body.Total)
	}
	if len(body.Users) != 2 {
		t.Errorf("got %d users, want 2", len(body.Users))
	}
	if body.Machines[0].Internal {
		t.Errorf("first machine should not be a funnel machine")
	}
	if !body.Machines[1].Internal {
		t.Errorf("second machine should be a funnel machine")
	}
}

func TestListMachinesFiltersByQUserStatus(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	cases := []struct {
		name      string
		qs        string
		wantCount int
		wantIDs   []string
	}{
		{"q matches name", "q=node1", 1, []string{"1"}},
		{"user filter", "user=alice", 1, []string{"1"}},
		{"status online", "status=online", 1, []string{"1"}},
		{"status expired", "status=expired", 1, []string{"2"}},
		{"no filters", "", 2, []string{"1", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines?"+tc.qs, nil), user), client)
			rec := httptest.NewRecorder()
			h.ListMachines(rec, req)

			var body struct {
				Machines []hscl.Node `json:"machines"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.Machines) != tc.wantCount {
				t.Fatalf("got %d machines, want %d", len(body.Machines), tc.wantCount)
			}
			for i, id := range tc.wantIDs {
				if body.Machines[i].ID != id {
					t.Errorf("machine[%d].ID = %q, want %q", i, body.Machines[i].ID, id)
				}
			}
		})
	}
}

func withPathValue(req *http.Request, key, value string) *http.Request {
	req.SetPathValue(key, value)
	return req
}

func TestRenameMachine(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":"new-name"}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/rename/new-name") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestRenameMachineRejectsEmptyName(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":""}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestSetMachineTagsNormalization covers normalizeTags: the frontend's
// dialog claims it adds the "tag:" prefix automatically, so the backend
// must actually do it (defense in depth for any API caller), regardless of
// case, whitespace, or duplicate/empty entries.
func TestSetMachineTagsNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		wantTags []string
	}{
		{"already prefixed", []string{"tag:a", "tag:b"}, []string{"tag:a", "tag:b"}},
		{"missing prefix is added", []string{"a", "tag:b"}, []string{"tag:a", "tag:b"}},
		{"whitespace and case are normalized", []string{" Tag:A ", "B"}, []string{"tag:a", "tag:b"}},
		{"empty and duplicate entries are dropped", []string{"a", "tag:a", "", "  "}, []string{"tag:a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			user, client, requests, _, lastTags := activeOrgUserWithPolicy(t, h, queries)

			payload, err := json.Marshal(map[string][]string{"tags": tt.input})
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", bytes.NewReader(payload)), user), client), "id", "1")
			rec := httptest.NewRecorder()
			h.SetMachineTags(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var tagsReqSeen bool
			for _, r := range *requests {
				if strings.HasSuffix(r.URL.Path, "/tags") {
					tagsReqSeen = true
				}
			}
			if !tagsReqSeen {
				t.Fatal("no request sent to .../tags")
			}
			if !slices.Equal(*lastTags, tt.wantTags) {
				t.Errorf("tags sent to headscale = %v, want %v", *lastTags, tt.wantTags)
			}
		})
	}
}

// TestSetMachineTagsProvisionsTagOwners covers ensureTagOwners: headscale
// rejects any tag missing from the ACL policy's tagOwners map (see
// hscontrol/state.State.SetNodeTags in headscale's source), and ownscale's
// seeded default policy never declares any tag -- so the handler must add
// missing tags to tagOwners itself before calling SetTags.
func TestSetMachineTagsProvisionsTagOwners(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, _, policy, _ := activeOrgUserWithPolicy(t, h, queries)

	if strings.Contains(*policy, "tagOwners") {
		t.Fatalf("test fixture precondition failed: policy already has tagOwners: %s", *policy)
	}

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var parsed struct {
		TagOwners map[string][]string `json:"tagOwners"`
	}
	if err := json.Unmarshal([]byte(*policy), &parsed); err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid JSON: %v: %s", err, *policy)
	}
	owners, ok := parsed.TagOwners["tag:server"]
	if !ok || len(owners) == 0 {
		t.Fatalf("policy tagOwners missing/empty entry for tag:server: %+v", parsed.TagOwners)
	}
}

// TestSetMachineTagsSkipsPolicyUpdateWhenTagAlreadyOwned covers the case
// where every requested tag is already declared: ensureTagOwners must not
// call SetPolicy needlessly.
func TestSetMachineTagsSkipsPolicyUpdateWhenTagAlreadyOwned(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, requests, policy, _ := activeOrgUserWithPolicy(t, h, queries)
	*policy = `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:server":["alice"]}}`

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, r := range *requests {
		if r.URL.Path == "/api/v1/policy" && r.Method == http.MethodPut {
			t.Fatal("SetPolicy should not be called when every tag is already in tagOwners")
		}
	}
}

// TestSetMachineTagsPreservesPolicyCommentsAndFormatting covers the actual
// bug: ensureTagOwners used to decode the policy into a map and re-encode
// it, which silently dropped comments and reordered/reformatted everything.
// It must now only patch in the new tagOwners entry, leaving the rest of
// the HuJSON document byte-for-byte untouched.
func TestSetMachineTagsPreservesPolicyCommentsAndFormatting(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, _, policy, _ := activeOrgUserWithPolicy(t, h, queries)
	original := "{\n" +
		"  // keep this comment\n" +
		"  \"acls\": [\n" +
		"    {\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]},\n" +
		"  ],\n" +
		"}\n"
	*policy = original

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(*policy, "// keep this comment") {
		t.Fatalf("policy sent to SetPolicy dropped the original comment: %s", *policy)
	}
	if !strings.Contains(*policy, `"action": "accept"`) {
		t.Fatalf("policy sent to SetPolicy reformatted the original acls entry: %s", *policy)
	}
	var parsed struct {
		TagOwners map[string][]string `json:"tagOwners"`
	}
	standardized, err := hujson.Standardize([]byte(*policy))
	if err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid HuJSON: %v: %s", err, *policy)
	}
	if err := json.Unmarshal(standardized, &parsed); err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid JSON: %v: %s", err, *policy)
	}
	if owners := parsed.TagOwners["tag:server"]; len(owners) == 0 {
		t.Fatalf("policy tagOwners missing/empty entry for tag:server: %+v", parsed.TagOwners)
	}
}

func TestSetMachineRoutes(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"routes":["10.0.0.0/24"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/routes", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/approve_routes") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestExpireMachine(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantPath string
	}{
		{"no body expires now", "", http.StatusOK, "/api/v1/node/1/expire"},
		{"mode now", `{"mode":"now"}`, http.StatusOK, "/api/v1/node/1/expire"},
		{"disable", `{"mode":"disable"}`, http.StatusOK, "/api/v2/device/1/key"},
		{"enable", `{"mode":"enable","expiry":"` + future + `"}`, http.StatusOK, "/api/v1/node/1/expire"},
		{"enable without expiry", `{"mode":"enable"}`, http.StatusBadRequest, ""},
		{"enable in the past", `{"mode":"enable","expiry":"2020-01-01T00:00:00Z"}`, http.StatusBadRequest, ""},
		{"unknown mode", `{"mode":"later"}`, http.StatusBadRequest, ""},
		{"invalid json", `{`, http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			user, client, requests := activeOrgUser(t, h, queries)
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/expire", body), user), client), "id", "1")
			rec := httptest.NewRecorder()
			h.ExpireMachine(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantPath == "" {
				return
			}
			last := (*requests)[len(*requests)-1]
			if last.URL.Path != tt.wantPath {
				t.Errorf("upstream path = %s, want %s", last.URL.Path, tt.wantPath)
			}
		})
	}
}

func TestDeleteMachine(t *testing.T) {
	h, queries := newTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	route, err := queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: user.OrganizationID,
		Label:          "web",
		TargetNodeID:   "2",
		TargetPort:     8080,
		CreatedBy:      sql.NullString{String: user.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/machines/1", nil), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if last.Method != http.MethodDelete {
		t.Errorf("expected DELETE upstream, got %s", last.Method)
	}

	// Node "1" is owned by "alice" (not the funnel user), so deleting it
	// must not touch the org's funnel routes.
	remaining, err := queries.ListFunnelRoutesByOrg(t.Context(), user.OrganizationID)
	if err != nil {
		t.Fatalf("ListFunnelRoutesByOrg: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != route.ID {
		t.Errorf("got %d funnel routes after deleting a normal machine, want the original route untouched", len(remaining))
	}
}

func TestDeleteMachineCascadesFunnelRoutesForInternalMachine(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner)
	queries, user := h.queries, users[0]

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"nodes":[
			{"id":"1","name":"node1","givenName":"node1","ipAddresses":["100.64.0.1"],
			 "user":{"id":"1","name":"alice"},"online":true,"createdAt":"2024-01-01T00:00:00Z"},
			{"id":"2","name":"funnel-acme","givenName":"funnel-acme","ipAddresses":["100.64.0.2"],
			 "user":{"id":"2","name":%q},"online":true,"createdAt":"2024-01-01T00:00:00Z"}
		]}`, funnelproxy.FunnelHeadscaleUserName)
	})
	var deletedNodeID string
	mux.HandleFunc("/api/v1/node/2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"node":{"id":"2","name":"funnel-acme","givenName":"funnel-acme",
				"user":{"id":"2","name":%q},"online":true,"createdAt":"2024-01-01T00:00:00Z"}}`,
				funnelproxy.FunnelHeadscaleUserName)
			return
		}
		deletedNodeID = "2"
		fmt.Fprint(w, `{}`)
	})
	client := hscl.NewUnix(hstest.Socket(t, mux))
	h.hs = fakeHeadscaleClients{client: client}

	for _, label := range []string{"web", "admin"} {
		if _, err := queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
			ID:             xid.New().String(),
			OrganizationID: org.ID,
			Label:          label,
			TargetNodeID:   "1",
			TargetPort:     8080,
			CreatedBy:      sql.NullString{String: user.ID, Valid: true},
		}); err != nil {
			t.Fatalf("CreateFunnelRoute(%q): %v", label, err)
		}
	}

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/machines/2", nil), user), client), "id", "2")
	rec := httptest.NewRecorder()
	h.DeleteMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if deletedNodeID != "2" {
		t.Errorf("expected node 2 to be deleted upstream, got %q", deletedNodeID)
	}

	remaining, err := queries.ListFunnelRoutesByOrg(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("ListFunnelRoutesByOrg: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("got %d funnel routes after deleting the internal funnel machine, want 0", len(remaining))
	}
}

func TestRegisterMachine(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantQuery  url.Values // only checked on 200
	}{
		{
			name:       "bare token",
			body:       `{"memberId":"{alice}","key":"abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"alice"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "already prefixed key",
			body:       `{"memberId":"{bob}","key":"hskey-authreq-abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"bob"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "full registration URL",
			body:       `{"memberId":"{alice}","key":"https://hs.example.test/register/abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"alice"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "missing member",
			body:       `{"key":"abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing key",
			body:       `{"memberId":"{alice}"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unrecognized key format",
			body:       `{"memberId":"{alice}","key":"not a key!!"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "headscale error propagates as 500",
			body:       `{"memberId":"{alice}","key":"hskey-authreq-failme00000000000000000000"}`,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, queries := newTestHandlers(t)
			user, client, requests := activeOrgUser(t, h, queries)
			// Members linked to the fake's headscale users alice (1) and bob (2).
			alice := callerWithRole(t, h, user, RoleMember, "1")
			bob := callerWithRole(t, h, user, RoleMember, "2")
			body := strings.NewReplacer("{alice}", alice.ID, "{bob}", bob.ID).Replace(tc.body)
			org, err := queries.GetOrganization(t.Context(), user.OrganizationID)
			if err != nil {
				t.Fatalf("GetOrganization: %v", err)
			}

			req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/register", strings.NewReader(body)), user), client, org)
			rec := httptest.NewRecorder()
			h.RegisterMachine(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantQuery != nil {
				last := (*requests)[len(*requests)-1]
				if got := last.URL.Query(); got.Get("user") != tc.wantQuery.Get("user") || got.Get("key") != tc.wantQuery.Get("key") {
					t.Errorf("upstream query = %v, want %v", got, tc.wantQuery)
				}
			}
		})
	}
}

func TestWithoutFunnelUser(t *testing.T) {
	users := []hscl.User{
		{ID: "1", Name: "alice"},
		{ID: "2", Name: funnelproxy.FunnelHeadscaleUserName},
		{ID: "3", Name: funnelproxy.FunnelHeadscaleUserName, ProviderID: "https://ownscale.test/abc"},
	}
	got := withoutFunnelUser(users)
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Errorf("withoutFunnelUser() = %+v, want users 1 and 3", got)
	}
}

func TestGetMachineByIP(t *testing.T) {
	h, org, users := newOrgTestHandlers(t, RoleOwner)
	client, _, _, _ := newFakeHeadscaleServer(t)

	tests := []struct {
		name string
		ip   string
		code int
	}{
		{"known address", "100.64.0.1", http.StatusOK},
		{"unknown address", "100.64.9.9", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/", nil), users[0]), client, org)
			req.SetPathValue("ip", tt.ip)
			rec := httptest.NewRecorder()
			h.GetMachineByIP(rec, req)
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.code, rec.Body.String())
			}
			if tt.code != http.StatusOK {
				return
			}
			var resp struct {
				Machine    struct{ ID string } `json:"machine"`
				DeviceInfo hscl.DeviceInfo     `json:"deviceInfo"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Machine.ID != "1" || resp.DeviceInfo.OS != "linux" || resp.DeviceInfo.ClientVersion != "1.80.0" {
				t.Errorf("response = %+v", resp)
			}
		})
	}
}
