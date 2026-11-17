package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vwa "github.com/descope/virtualwebauthn"
	"github.com/ownscalenet/ownscale/derrors"
	"golang.org/x/sync/errgroup"
)

const (
	authKeyExpiryDays = 90
	// provisionParallelism bounds concurrent sign-ups, each of which starts
	// a new headscale on the server.
	provisionParallelism = 4
	apiTimeout           = 2 * time.Minute
)

var errUnauthorized = errors.New("unauthorized")

// orgState is what's cached about one stress organization, so later runs
// sign back in to the same account and reuse its key instead of creating more.
type orgState struct {
	Username      string            `json:"username"`
	Authenticator vwa.Authenticator `json:"authenticator"`
	Cookies       []savedCookie     `json:"cookies"`
	Subdomain     string            `json:"subdomain"`
	MemberID      string            `json:"memberId"`
	AuthKeyID     string            `json:"authKeyId"`
	AuthKey       string            `json:"authKey"`
}

type savedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// stateFile is orgs.json in the state dir, one entry per org index.
type stateFile struct {
	path string
	mu   sync.Mutex
	orgs []*orgState
}

func loadState(dir string) (_ *stateFile, err error) {
	defer derrors.Wrap(&err, "loadState(%q)", dir)
	sf := &stateFile{path: filepath.Join(dir, "orgs.json")}
	b, err := os.ReadFile(sf.path)
	if errors.Is(err, os.ErrNotExist) {
		return sf, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &sf.orgs); err != nil {
		return nil, err
	}
	return sf, nil
}

// entry returns the state for org i, growing the list as needed.
func (sf *stateFile) entry(i int) *orgState {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	for len(sf.orgs) <= i {
		sf.orgs = append(sf.orgs, &orgState{})
	}
	return sf.orgs[i]
}

// save writes the whole file through a rename, so an interrupted run never
// leaves it half written.
func (sf *stateFile) save() (err error) {
	defer derrors.Wrap(&err, "sf.save()")
	sf.mu.Lock()
	defer sf.mu.Unlock()
	b, err := json.MarshalIndent(sf.orgs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sf.path), 0o700); err != nil {
		return err
	}
	tmp := sf.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sf.path)
}

// org is a provisioned organization ready for machines to join.
type org struct {
	index     int
	subdomain string
	authKey   string
	console   *consoleClient
}

// provisionOrgs signs up, or signs back in to, orgs 0..cfg.orgs-1 and makes
// sure each has a valid reusable auth key, caching everything in sf.
func provisionOrgs(ctx context.Context, cfg config, sf *stateFile, out io.Writer) (_ []*org, err error) {
	defer derrors.Wrap(&err, "provisionOrgs()")
	orgs := make([]*org, cfg.orgs)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(provisionParallelism)
	for i := range cfg.orgs {
		g.Go(func() error {
			st := sf.entry(i)
			c, err := newConsoleClient(cfg.baseDomain, st)
			if err != nil {
				return err
			}
			action, err := c.ensure(ctx, sf)
			if err != nil {
				return fmt.Errorf("org %d (%s): %w", i, cmp.Or(st.Username, "new"), err)
			}
			fmt.Fprintf(out, "org %d: %s %s (%s)\n", i, action, st.Subdomain, st.Username)
			orgs[i] = &org{index: i, subdomain: st.Subdomain, authKey: st.AuthKey, console: c}
			return nil
		})
	}
	return orgs, g.Wait()
}

// consoleClient talks to the console API as one org's owner.
type consoleClient struct {
	base       *url.URL
	baseDomain string
	rp         vwa.RelyingParty
	http       *http.Client
	st         *orgState
}

func newConsoleClient(baseDomain string, st *orgState) (*consoleClient, error) {
	host := "console." + baseDomain
	base := &url.URL{Scheme: "https", Host: host}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	var cookies []*http.Cookie
	for _, c := range st.Cookies {
		cookies = append(cookies, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	jar.SetCookies(base, cookies)
	return &consoleClient{
		base:       base,
		baseDomain: baseDomain,
		rp:         vwa.RelyingParty{Name: "Ownscale", ID: host, Origin: base.String()},
		http: &http.Client{
			Jar:     jar,
			Timeout: apiTimeout,
			// Logins answer with a redirect for the browser; the session
			// cookie on that response is all we need.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		st: st,
	}, nil
}

// ensure brings c's org to a usable state and reports what it had to do.
func (c *consoleClient) ensure(ctx context.Context, sf *stateFile) (action string, err error) {
	action = "reused"
	if c.st.Username == "" {
		if err := c.signUp(ctx); err != nil {
			return "", err
		}
		action = "signed up"
		if err := sf.save(); err != nil {
			return "", err
		}
	}
	var sess struct {
		User         struct{ ID string }
		Organization struct{ Domain string }
	}
	err = c.do(ctx, http.MethodGet, "/api/session", nil, &sess)
	if errors.Is(err, errUnauthorized) {
		if err := c.login(ctx); err != nil {
			return "", err
		}
		if action == "reused" {
			action = "signed in to"
		}
		err = c.do(ctx, http.MethodGet, "/api/session", nil, &sess)
	}
	if err != nil {
		return "", err
	}
	c.st.MemberID = sess.User.ID
	c.st.Subdomain = strings.TrimSuffix(sess.Organization.Domain, "."+c.baseDomain)
	c.saveCookies()

	ok, err := c.authKeyValid(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		if err := c.createAuthKey(ctx); err != nil {
			return "", err
		}
	}
	return action, sf.save()
}

func (c *consoleClient) signUp(ctx context.Context) error {
	username := fmt.Sprintf("stress-%s", strings.ToLower(rand.Text()[:8]))
	opts, err := c.beginCeremony(ctx, "/api/passkey/register/begin", map[string]string{"username": username})
	if err != nil {
		return err
	}
	attOpts, err := vwa.ParseAttestationOptions(opts)
	if err != nil {
		return err
	}
	auth := vwa.NewAuthenticator()
	auth.Options.UserHandle = []byte(attOpts.UserID)
	cred := vwa.NewCredential(vwa.KeyTypeEC2)
	if err := c.finishCeremony(ctx, "/api/passkey/register/finish", vwa.CreateAttestationResponse(c.rp, auth, cred, *attOpts)); err != nil {
		return err
	}
	auth.AddCredential(cred)
	c.st.Username = username
	c.st.Authenticator = auth
	c.saveCookies()
	return nil
}

func (c *consoleClient) login(ctx context.Context) error {
	opts, err := c.beginCeremony(ctx, "/api/passkey/login/begin", map[string]string{})
	if err != nil {
		return err
	}
	asOpts, err := vwa.ParseAssertionOptions(opts)
	if err != nil {
		return err
	}
	if len(c.st.Authenticator.Credentials) == 0 {
		return errors.New("no cached passkey")
	}
	cred := c.st.Authenticator.Credentials[0]
	if err := c.finishCeremony(ctx, "/api/passkey/login/finish", vwa.CreateAssertionResponse(c.rp, c.st.Authenticator, cred, *asOpts)); err != nil {
		return err
	}
	c.saveCookies()
	return nil
}

func (c *consoleClient) beginCeremony(ctx context.Context, path string, body any) (string, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

// finishCeremony posts the authenticator's response; success is a redirect
// that sets the session cookie.
func (c *consoleClient) finishCeremony(ctx context.Context, path, response string) error {
	resp, err := c.request(ctx, http.MethodPost, path, strings.NewReader(response))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		return apiError(path, resp)
	}
	return nil
}

func (c *consoleClient) authKeyValid(ctx context.Context) (bool, error) {
	if c.st.AuthKeyID == "" {
		return false, nil
	}
	var list struct {
		AuthKeys []struct {
			ID      string `json:"id"`
			Expired bool   `json:"expired"`
		} `json:"authKeys"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/auth-keys", nil, &list); err != nil {
		return false, err
	}
	for _, k := range list.AuthKeys {
		if k.ID == c.st.AuthKeyID {
			return !k.Expired, nil
		}
	}
	return false, nil
}

func (c *consoleClient) createAuthKey(ctx context.Context) error {
	var resp struct {
		AuthKey struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"authKey"`
	}
	body := map[string]any{"memberId": c.st.MemberID, "reusable": true, "expiryDays": authKeyExpiryDays}
	if err := c.do(ctx, http.MethodPost, "/api/auth-keys", body, &resp); err != nil {
		return err
	}
	c.st.AuthKeyID, c.st.AuthKey = resp.AuthKey.ID, resp.AuthKey.Key
	return nil
}

// machineCount returns how many machines the org's headscale has.
func (c *consoleClient) machineCount(ctx context.Context) (int, error) {
	var list struct {
		Total int `json:"total"`
	}
	err := c.do(ctx, http.MethodGet, "/api/machines", nil, &list)
	return list.Total, err
}

func (c *consoleClient) saveCookies() {
	c.st.Cookies = nil
	for _, ck := range c.http.Jar.Cookies(c.base) {
		c.st.Cookies = append(c.st.Cookies, savedCookie{Name: ck.Name, Value: ck.Value})
	}
}

// do sends a JSON request and decodes a 200 response into out. A sleeping
// org's headscale answers 503 while it wakes, so those are retried.
func (c *consoleClient) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.request(ctx, method, path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusServiceUnavailable && attempt < 10 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return json.NewDecoder(resp.Body).Decode(out)
		case http.StatusUnauthorized:
			return errUnauthorized
		default:
			return apiError(path, resp)
		}
	}
}

func (c *consoleClient) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}

func apiError(path string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: %s: %s", path, resp.Status, strings.TrimSpace(string(b)))
}
