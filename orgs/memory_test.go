package orgs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"testing"

	"github.com/ownscalenet/ownscale/debugserver"
)

// TestMemoryAfterSleep is a diagnostic: it starts and sleeps orgs and logs
// memory per phase. Set OWNSCALE_MEMTEST_DIR to also dump profiles there.
func TestMemoryAfterSleep(t *testing.T) {
	dir := os.Getenv("OWNSCALE_MEMTEST_DIR")
	if dir == "" {
		t.Skip("OWNSCALE_MEMTEST_DIR not set")
	}
	logMem := func(name string) {
		m := debugserver.ReadMemStats()
		t.Logf("%-14s rss=%5.1fMB anon=%5.1fMB heapInuse=%5.1fMB heapIdle=%5.1fMB released=%5.1fMB stack=%5.1fMB sys=%5.1fMB goroutines=%d",
			name, mb(m.RSS), mb(m.RSSAnon), mb(m.HeapInuse), mb(m.HeapIdle), mb(m.HeapReleased), mb(m.StackInuse), mb(m.Sys), m.Goroutines)
	}
	snap := func(name string) {
		// A plain GC shows what an idle process retains; FreeOSMemory shows the floor.
		runtime.GC()
		logMem(name + "/gc")
		debug.FreeOSMemory()
		logMem(name + "/free")
		for _, p := range []string{"heap", "goroutine"} {
			f, err := os.Create(filepath.Join(dir, name+"."+p))
			if err != nil {
				t.Fatal(err)
			}
			if err := pprof.Lookup(p).WriteTo(f, 0); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}

	svc := newRealService(t)
	snap("baseline")
	const n = 5
	for cycle := range 3 {
		var orgs []*OrgService
		for i := range n {
			sub := fmt.Sprintf("org%d", i)
			if cycle == 0 {
				orgs = append(orgs, svc.ForOrg(createStarted(t, svc, sub)))
				continue
			}
			if _, err := svc.Wake(t.Context(), sub); err != nil {
				t.Fatalf("Wake: %v", err)
			}
			org, err := svc.queries.GetOrganizationBySubdomain(t.Context(), sub)
			if err != nil {
				t.Fatal(err)
			}
			orgs = append(orgs, svc.ForOrg(org))
		}
		snap(fmt.Sprintf("running%d", cycle))
		for _, o := range orgs {
			if err := o.Sleep(t.Context()); err != nil {
				t.Fatalf("Sleep: %v", err)
			}
		}
		snap(fmt.Sprintf("asleep%d", cycle))
	}
}

func mb[T int64 | uint64](v T) float64 { return float64(v) / (1 << 20) }
