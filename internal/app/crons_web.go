package app

import (
	"net/http"
	"sync"
	"time"
)

// cronsCacheTTL keeps the Crons tab from re-running SSH probes on every load;
// ?refresh=1 collects again.
const cronsCacheTTL = time.Minute

var viewCrons struct {
	sync.Mutex
	result cronsScanResult
	at     time.Time
}

func (s *configWebServer) handleCrons(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	viewCrons.Lock()
	defer viewCrons.Unlock()
	if viewCrons.at.IsZero() || time.Since(viewCrons.at) > cronsCacheTTL || r.URL.Query().Get("refresh") == "1" {
		result, err := collectCrons(s.doc.sharedPath, nil)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "crons_failed", err.Error())
			return
		}
		viewCrons.result, viewCrons.at = result, time.Now()
	}
	writeJSON(w, http.StatusOK, viewCrons.result)
}

// patchCronScan puts a fresh record into the cached scan in place of the rows
// drop matches (at the first one's place), or removes them when fresh is nil,
// so the next action on that row finds it as it now is. The next GET still
// collects again.
func patchCronScan(drop func(cronJob) bool, fresh *cronJob) {
	viewCrons.Lock()
	defer viewCrons.Unlock()
	jobs := make([]cronJob, 0, len(viewCrons.result.Jobs)+1)
	placed := fresh == nil
	for _, job := range viewCrons.result.Jobs {
		if !drop(job) {
			jobs = append(jobs, job)
		} else if !placed {
			jobs, placed = append(jobs, *fresh), true
		}
	}
	if !placed {
		jobs = append(jobs, *fresh)
	}
	viewCrons.result.Jobs = jobs
	viewCrons.at = time.Time{}
}

// invalidateCronScan makes the next GET collect again.
func invalidateCronScan() {
	viewCrons.Lock()
	viewCrons.at = time.Time{}
	viewCrons.Unlock()
}
