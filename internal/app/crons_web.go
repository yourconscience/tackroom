package app

import (
	"net/http"
	"sync"
	"time"
)

// cronsCacheTTL keeps the Crons tab from re-running SSH probes on every load;
// ?refresh=1 collects again.
const cronsCacheTTL = time.Minute

// viewCrons is held only to read or change the stored scan, never during a
// collect, so an action does not wait for one. gen counts patches and
// invalidations: a collect that began before one does not store what it read.
var viewCrons struct {
	sync.Mutex
	result cronsScanResult
	at     time.Time
	gen    int
}

// viewCronsCollect lets one collect run at a time.
var viewCronsCollect sync.Mutex

func (s *configWebServer) handleCrons(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	asked := time.Now()
	viewCronsCollect.Lock()
	defer viewCronsCollect.Unlock()
	viewCrons.Lock()
	result, gen := viewCrons.result, viewCrons.gen
	// A refresh that waited for a collect stored after it asked takes that one.
	collect := viewCrons.at.IsZero() || time.Since(viewCrons.at) > cronsCacheTTL || (r.URL.Query().Get("refresh") == "1" && viewCrons.at.Before(asked))
	viewCrons.Unlock()
	if collect {
		var err error
		if result, err = collectCrons(s.doc.sharedPath, nil); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "crons_failed", err.Error())
			return
		}
		viewCrons.Lock()
		if viewCrons.gen == gen {
			viewCrons.result, viewCrons.at = result, time.Now()
		}
		viewCrons.Unlock()
	}
	writeJSON(w, http.StatusOK, result)
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
	viewCrons.gen++
}

// invalidateCronScan makes the next GET collect again.
func invalidateCronScan() {
	viewCrons.Lock()
	viewCrons.at = time.Time{}
	viewCrons.gen++
	viewCrons.Unlock()
}
