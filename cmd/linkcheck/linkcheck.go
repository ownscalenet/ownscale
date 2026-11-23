package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ownscalenet/ownscale/derrors"
)

// run checks every console-to-docs link found under frontendSrcDir against
// the doc pages built into docsDistDir, and every docs-to-console link
// found under docsContentDir against the console routes declared in
// App.tsx. It returns a combined error listing every stale link found.
func run(frontendSrcDir, docsContentDir, docsDistDir string) (err error) {
	defer derrors.Wrap(&err, "run(%q, %q, %q)", frontendSrcDir, docsContentDir, docsDistDir)

	routes, err := consoleRoutes(filepath.Join(frontendSrcDir, "App.tsx"))
	if err != nil {
		return err
	}
	docLinks, err := findDocLinks(frontendSrcDir)
	if err != nil {
		return err
	}
	consoleLinks, err := findConsoleLinks(docsContentDir)
	if err != nil {
		return err
	}

	docsDist := os.DirFS(docsDistDir)
	var problems []string
	for _, l := range docLinks {
		if err := l.validate(docsDist); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", l.source, err))
		}
	}
	for _, l := range consoleLinks {
		if !matchesRoute(routes, l.path) {
			problems = append(problems,
				fmt.Sprintf("%s: console route %q doesn't exist", l.source, l.path))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New("\n" + strings.Join(problems, "\n"))
}

var routeRe = regexp.MustCompile(`<Route\s+(?:index\b|path="([^"]*)")`)

// consoleRoutes extracts every path declared in a <Route> in the React
// Router config at appTSXPath, as used by App.tsx. An index route (no
// path) is reported as "/". Assumes a single level of route nesting, which
// is all App.tsx currently has.
func consoleRoutes(appTSXPath string) (_ []string, err error) {
	defer derrors.Wrap(&err, "consoleRoutes(%q)", appTSXPath)

	data, err := os.ReadFile(appTSXPath)
	if err != nil {
		return nil, err
	}
	var routes []string
	for _, m := range routeRe.FindAllStringSubmatch(string(data), -1) {
		if m[1] == "" {
			routes = append(routes, "/")
			continue
		}
		routes = append(routes, m[1])
	}
	return routes, nil
}

// matchesRoute reports whether consolePath (e.g. "/access-control" or
// "/machines/123/ssh") matches one of routes, treating a ":param" route
// segment as matching any single path segment. The catch-all "*" route
// never matches, since it would make every console link trivially valid.
func matchesRoute(routes []string, consolePath string) bool {
	want := strings.Split(strings.Trim(consolePath, "/"), "/")
	if consolePath == "" || consolePath == "/" {
		want = nil
	}
	for _, route := range routes {
		if route == "*" {
			continue
		}
		have := strings.Split(strings.Trim(route, "/"), "/")
		if route == "" || route == "/" {
			have = nil
		}
		if routeSegmentsMatch(have, want) {
			return true
		}
	}
	return false
}

func routeSegmentsMatch(have, want []string) bool {
	if len(have) != len(want) {
		return false
	}
	for i, seg := range have {
		if strings.HasPrefix(seg, ":") {
			continue
		}
		if seg != want[i] {
			return false
		}
	}
	return true
}

// docLink is a link from console source code to a docs page, optionally to
// an anchor within it.
type docLink struct {
	file   string // docs/dist-relative path to the HTML page, e.g. "docs/use-cases/funnels/index.html"
	anchor string // required heading id within file, if any
	source string // "<frontend file>:<line>", for error messages
}

// validate reports an error if l's page doesn't exist in docsDist, or its
// anchor isn't a heading id on that page.
func (l docLink) validate(docsDist fs.FS) (err error) {
	defer derrors.Wrap(&err, "docLink.validate(%q)", l.file)

	data, err := fs.ReadFile(docsDist, l.file)
	if err != nil {
		return fmt.Errorf("doc page %q doesn't exist (or docs aren't built: run `make docs`)", l.file)
	}
	// Minified HTML drops quotes around attribute values, so an anchor's id
	// can appear as id="foo" or id=foo.
	if l.anchor != "" && !regexp.MustCompile(`id=['"]?`+regexp.QuoteMeta(l.anchor)+`['"\s>]`).Match(data) {
		return fmt.Errorf("doc page %q has no #%s anchor", l.file, l.anchor)
	}
	return nil
}

var (
	docPageURLRe = regexp.MustCompile(`docPageURL\(\s*'([^']*)'(?:\s*,\s*'([^']*)')?\s*\)`)
	faqURLRe     = regexp.MustCompile(`faqURL\(\s*'([^']*)'\s*\)`)
	shortURLRe   = regexp.MustCompile(`shortURL\(\s*'([^']*)'\s*\)`)
	docsURLRe    = regexp.MustCompile(`docsURL\(\s*\)`)
	siteURLRe    = regexp.MustCompile(`siteURL\(\s*\)`)
)

// findDocLinks walks every .ts/.tsx file under srcDir and collects a
// docLink for each call to lib/faq.ts's URL helpers (docPageURL, faqURL,
// shortURL, docsURL, siteURL).
func findDocLinks(srcDir string) (_ []docLink, err error) {
	defer derrors.Wrap(&err, "findDocLinks(%q)", srcDir)

	var links []docLink
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			source := fmt.Sprintf("%s:%d", path, i+1)
			for _, m := range docPageURLRe.FindAllStringSubmatch(line, -1) {
				links = append(links, docLink{
					file:   "docs/" + m[1] + "/index.html",
					anchor: m[2],
					source: source,
				})
			}
			for _, m := range faqURLRe.FindAllStringSubmatch(line, -1) {
				links = append(links, docLink{file: "docs/faqs/index.html", anchor: m[1], source: source})
			}
			for _, m := range shortURLRe.FindAllStringSubmatch(line, -1) {
				links = append(links, docLink{file: "s/" + m[1] + "/index.html", source: source})
			}
			if docsURLRe.MatchString(line) {
				links = append(links, docLink{file: "docs/index.html", source: source})
			}
			if siteURLRe.MatchString(line) {
				links = append(links, docLink{file: "index.html", source: source})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return links, nil
}

// consoleLink is a link from docs content to a console route.
type consoleLink struct {
	path   string // the part of the URL after "/console", e.g. "/access-control"
	source string // "<docs file>:<line>", for error messages
}

var (
	consoleMDLinkRe   = regexp.MustCompile(`\]\(/console([^)\s]*)\)`)
	consoleCodeLinkRe = regexp.MustCompile("`/console([^`]*)`")
)

// findConsoleLinks walks every .md file under docsContentDir and collects a
// consoleLink for each Markdown or inline-code link to a /console/... URL.
func findConsoleLinks(docsContentDir string) (_ []consoleLink, err error) {
	defer derrors.Wrap(&err, "findConsoleLinks(%q)", docsContentDir)

	var links []consoleLink
	walkErr := filepath.WalkDir(docsContentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			source := fmt.Sprintf("%s:%d", path, i+1)
			for _, re := range [...]*regexp.Regexp{consoleMDLinkRe, consoleCodeLinkRe} {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					p := m[1]
					if i := strings.IndexAny(p, "?#"); i >= 0 {
						p = p[:i]
					}
					links = append(links, consoleLink{path: p, source: source})
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return links, nil
}
