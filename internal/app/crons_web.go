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
