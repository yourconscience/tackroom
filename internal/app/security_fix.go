package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// securityFixResolver returns the advisories that name r's version as their
// fixed version; tests and demos can replace it.
var securityFixResolver = osvFixedAdvisories

const osvQueryURL = "https://api.osv.dev/v1/query"

// allowSecurityFix lets an npm or PyPI version through its cooldown when a
// published advisory names that version as the fix: waiting out the cooldown
// would keep a known hole open for three days. The decision is recorded in the
// lock as if --allow-fresh had been passed, with the advisory ids as the reason.
func allowSecurityFix(lock *lockFile, item, ref string, r packageReference, now time.Time) bool {
	idx := cooldownIndex(*lock, item, ref)
	if idx < 0 {
		return false
	}
	ids, err := securityFixResolver(r)
	if err != nil || len(ids) == 0 {
		return false
	}
	reason := "security fix: " + strings.Join(ids, ", ")
	lock.Cooldowns[idx].Allowed = &cooldownAllowed{At: now.UTC().Format(time.RFC3339), Reason: reason}
	fmt.Fprintf(os.Stderr, "cooldown skipped for %s (%s): %s\n", item, ref, reason)
	return true
}

type osvVuln struct {
	ID        string   `json:"id"`
	Aliases   []string `json:"aliases"`
	Withdrawn string   `json:"withdrawn"`
	Affected  []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Fixed string `json:"fixed"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// osvFixedAdvisories asks OSV (which carries the reviewed GitHub advisories)
// for the package's advisories and keeps those whose fixed version is r's.
func osvFixedAdvisories(r packageReference) ([]string, error) {
	ecosystem := "npm"
	if r.Ecosystem == ecosystemPyPI {
		ecosystem = "PyPI"
	}
	want := strings.TrimPrefix(r.Version, "v")
	client := &http.Client{Timeout: 15 * time.Second}
	var ids []string
	token := ""
	for page := 0; page < 10; page++ {
		body := map[string]any{"package": map[string]string{"name": r.Package, "ecosystem": ecosystem}}
		if token != "" {
			body["page_token"] = token
		}
		data, _ := json.Marshal(body)
		resp, err := client.Post(osvQueryURL, "application/json", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		var doc struct {
			Vulns []osvVuln `json:"vulns"`
			Next  string    `json:"next_page_token"`
		}
		err = json.NewDecoder(resp.Body).Decode(&doc)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("osv returned %s", resp.Status)
		}
		if err != nil {
			return nil, err
		}
		for _, v := range doc.Vulns {
			if v.Withdrawn == "" && fixesVersion(v, r.Package, ecosystem, want) {
				ids = append(ids, advisoryID(v))
			}
		}
		if token = doc.Next; token == "" {
			break
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func fixesVersion(v osvVuln, pkg, ecosystem, version string) bool {
	for _, a := range v.Affected {
		if a.Package.Ecosystem != ecosystem || !samePackage(a.Package.Name, pkg, ecosystem) {
			continue
		}
		for _, rg := range a.Ranges {
			for _, e := range rg.Events {
				if e.Fixed != "" && strings.TrimPrefix(e.Fixed, "v") == version {
					return true
				}
			}
		}
	}
	return false
}

var pypiSeparators = regexp.MustCompile(`[-_.]+`)

// samePackage compares names the way the registry does: PyPI treats any run
// of "-", "_" and "." as one separator (PEP 503), so my_tool is my-tool.
func samePackage(a, b, ecosystem string) bool {
	if ecosystem == "PyPI" {
		a, b = pypiSeparators.ReplaceAllString(a, "-"), pypiSeparators.ReplaceAllString(b, "-")
	}
	return strings.EqualFold(a, b)
}

// advisoryID prefers the GHSA id, which is what release notes cite.
func advisoryID(v osvVuln) string {
	if strings.HasPrefix(v.ID, "GHSA-") {
		return v.ID
	}
	for _, a := range v.Aliases {
		if strings.HasPrefix(a, "GHSA-") {
			return a
		}
	}
	return v.ID
}
