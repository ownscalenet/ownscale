package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/juanfont/headscale/hscontrol/derp"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/juanfont/headscale/hscontrol/types/change"
	"tailscale.com/tailcfg"

	"github.com/ownscalenet/ownscale/derrors"
)

const (
	// derpMapInterval is how often RunDERPMapUpdater refetches the DERP map.
	derpMapInterval = 3 * time.Hour
	// derpMapFetchTimeout bounds one DERP map request.
	derpMapFetchTimeout = 30 * time.Second
	// maxDERPMapSize bounds the DERP map response body.
	maxDERPMapSize = 4 << 20
)

// derpMapClient fetches the DERP map.
var derpMapClient = &http.Client{Timeout: derpMapFetchTimeout}

// fetchDERPMap downloads and decodes the DERP map at url.
func fetchDERPMap(ctx context.Context, client *http.Client, url string) (_ *tailcfg.DERPMap, err error) {
	defer derrors.Wrap(&err, "fetchDERPMap(%q)", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	var dm tailcfg.DERPMap
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDERPMapSize)).Decode(&dm); err != nil {
		return nil, err
	}
	if len(dm.Regions) == 0 {
		return nil, errors.New("DERP map has no regions")
	}
	return &dm, nil
}

// currentDERPMap returns the last fetched DERP map, fetching it now if the
// updater hasn't yet, so orgs can start right after boot.
func (s *Service) currentDERPMap(ctx context.Context) (*tailcfg.DERPMap, error) {
	if dm := s.derpMap.Load(); dm != nil {
		return dm, nil
	}
	dm, err := fetchDERPMap(ctx, derpMapClient, s.cfg.Derp.MapURL)
	if err != nil {
		return nil, err
	}
	s.derpMap.CompareAndSwap(nil, dm)
	return s.derpMap.Load(), nil
}

// RunDERPMapUpdater fetches the DERP map now and every derpMapInterval,
// and pushes it to running headscale instances, until ctx is done.
func (s *Service) RunDERPMapUpdater(ctx context.Context) {
	t := time.NewTicker(derpMapInterval)
	defer t.Stop()
	for {
		if err := s.refreshDERPMap(ctx); err != nil {
			s.logger.Warn("refresh DERP map; keeping the previous one", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshDERPMap fetches the DERP map and pushes it to running instances.
func (s *Service) refreshDERPMap(ctx context.Context) error {
	dm, err := fetchDERPMap(ctx, derpMapClient, s.cfg.Derp.MapURL)
	if err != nil {
		return err
	}
	s.logger.Info("fetched DERP map", "regions", len(dm.Regions))

	s.derpMap.Store(dm)
	for subdomain := range s.orgs.Range {
		if err := s.pushDERPMap(subdomain, dm); err != nil {
			return err
		}
	}
	return nil
}

// pushDERPMap shuffles dm for subdomain and pushes it to subdomain's
// running instance, if any. It holds subdomain's orgLock so it doesn't race
// a concurrent restart/sleep of the same organization.
func (s *Service) pushDERPMap(subdomain string, dm *tailcfg.DERPMap) error {
	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()

	inst, ok := s.running(subdomain)
	if !ok {
		return nil
	}
	// GetDERPMap clones and shuffles the map for this instance.
	instMap, err := derp.GetDERPMap(types.DERPConfig{DERPMap: dm}, subdomain)
	if err != nil {
		return err
	}
	inst.app.GetState().SetDERPMap(instMap)
	inst.app.Change(change.DERPMap())
	return nil
}
