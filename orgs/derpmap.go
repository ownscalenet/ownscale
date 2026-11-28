package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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

	// BuiltinDERPMapPath is where the console serves BuiltinDERPMap.
	BuiltinDERPMapPath = "/derpmap/default"

	// builtinDERPRegionID is the region of the relays run by ownscale.net.
	// Tailscale leaves 900-999 for custom regions.
	builtinDERPRegionID = 900
	// firstCustomDERPRegionID is the region of an organization's first own
	// DERP server; each server gets its own region, as they don't mesh.
	firstCustomDERPRegionID = 901
)

// derpMapClient fetches Tailscale's DERP map.
var derpMapClient = &http.Client{Timeout: derpMapFetchTimeout}

// BuiltinDERPMap returns the DERP map of the relays run by ownscale.net.
func BuiltinDERPMap() *tailcfg.DERPMap {
	return &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{
		builtinDERPRegionID: {
			RegionID:   builtinDERPRegionID,
			RegionCode: "ownscale-eu",
			RegionName: "Ownscale EU",
			Nodes: []*tailcfg.DERPNode{{
				Name:     "900a",
				RegionID: builtinDERPRegionID,
				HostName: "derp1eu.ownscale.net",
				DERPPort: 443,
				STUNPort: 3478,
			}},
		},
	}}
}

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

// currentDERPMap returns the last fetched Tailscale DERP map, fetching it
// now if the updater hasn't yet, so orgs can start right after boot.
func (s *Service) currentDERPMap(ctx context.Context) (*tailcfg.DERPMap, error) {
	if dm := s.derpMap.Load(); dm != nil {
		return dm, nil
	}
	dm, err := fetchDERPMap(ctx, derpMapClient, s.cfg.DERPMapURL)
	if err != nil {
		return nil, err
	}
	s.derpMap.CompareAndSwap(nil, dm)
	return s.derpMap.Load(), nil
}

// derpMapFor builds the DERP map of an organization with settings d.
func (s *Service) derpMapFor(ctx context.Context, d DERPSettings) (*tailcfg.DERPMap, error) {
	dm := &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{}}
	if d.Tailscale {
		def, err := s.currentDERPMap(ctx)
		if err != nil {
			return nil, err
		}
		maps.Copy(dm.Regions, def.Regions)
		dm.HomeParams = def.HomeParams
	}
	if d.Ownscale {
		maps.Copy(dm.Regions, BuiltinDERPMap().Regions)
	}
	for i, srv := range d.Servers {
		id := tailcfg.DERPRegionID(firstCustomDERPRegionID + i)
		dm.Regions[id] = &tailcfg.DERPRegion{
			RegionID:   id,
			RegionCode: fmt.Sprintf("custom%d", i+1),
			RegionName: srv.HostName,
			Nodes: []*tailcfg.DERPNode{{
				Name:     fmt.Sprintf("%da", id),
				RegionID: id,
				HostName: srv.HostName,
				IPv4:     srv.IPv4,
				IPv6:     srv.IPv6,
			}},
		}
	}
	return dm, nil
}

// RunDERPMapUpdater fetches Tailscale's DERP map now and every
// derpMapInterval, and pushes running headscale instances their maps,
// until ctx is done.
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

// refreshDERPMap fetches Tailscale's DERP map and pushes running instances
// their maps.
func (s *Service) refreshDERPMap(ctx context.Context) error {
	dm, err := fetchDERPMap(ctx, derpMapClient, s.cfg.DERPMapURL)
	if err != nil {
		return err
	}
	s.logger.Info("fetched DERP map", "regions", len(dm.Regions))

	s.derpMap.Store(dm)
	var errs []error
	for subdomain := range s.orgs.Range {
		errs = append(errs, s.pushDERPMap(ctx, subdomain, nil))
	}
	return errors.Join(errs...)
}

// pushDERPMap pushes subdomain's running instance, if any, the DERP map of
// settings d, or of its current settings if d is nil. It holds subdomain's
// orgLock so it doesn't race a restart/sleep of the same organization.
func (s *Service) pushDERPMap(ctx context.Context, subdomain string, d *DERPSettings) error {
	s.orgLock(subdomain).Lock()
	defer s.orgLock(subdomain).Unlock()

	inst, ok := s.running(subdomain)
	if !ok {
		return nil
	}
	if d == nil {
		d = inst.derp.Load()
	}
	dm, err := s.derpMapFor(ctx, *d)
	if err != nil {
		return err
	}
	// GetDERPMap clones and shuffles the map for this instance.
	instMap, err := derp.GetDERPMap(ctx, types.DERPConfig{DERPMap: dm}, subdomain)
	if err != nil {
		return err
	}
	inst.derp.Store(d)
	inst.app.GetState().SetDERPMap(instMap)
	inst.app.Change(change.DERPMap())
	return nil
}
