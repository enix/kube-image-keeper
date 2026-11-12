package admin

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const pingEvery = 15 * time.Second

// parseFilter reads host, repo, kind, caller, status and since. It also
// returns the non-empty ones it consumed, as the query that selects the same
// requests.
func parseFilter(q url.Values) (f reqlog.Filter, used url.Values, err error) {
	used = url.Values{}
	for _, k := range []string{"host", "repo", "kind", "caller", "status", "since"} {
		if v := q.Get(k); v != "" {
			used.Set(k, v)
		}
	}
	f = reqlog.Filter{Host: q.Get("host"), Repo: q.Get("repo"), Caller: q.Get("caller"), Kind: api.Kind(q.Get("kind"))}
	if f.Kind != "" && !f.Kind.Valid() {
		return f, used, invalid("unknown kind %q", f.Kind)
	}
	if s := q.Get("status"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return f, used, invalid("status %q is not a number", s)
		}
		f.Status = &n
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, used, invalid("since %q is not an RFC 3339 time", s)
		}
		f.Since = t
	}
	return f, used, nil
}

func requestRoutes(mux *http.ServeMux, log *reqlog.Log) {
	route(mux, "GET /requests", func(w http.ResponseWriter, r *http.Request) error {
		f, _, err := parseFilter(r.URL.Query())
		if err != nil {
			return err
		}
		return ok(w, nonNil(log.Query(f)))
	})
	route(mux, "GET /requests/export", func(w http.ResponseWriter, r *http.Request) error {
		f, _, err := parseFilter(r.URL.Query())
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		bw := bufio.NewWriter(w)
		enc := json.NewEncoder(bw)
		for _, e := range log.Query(f) {
			if err := enc.Encode(e); err != nil {
				return nil // client gone
			}
		}
		_ = bw.Flush()
		return nil
	})
	route(mux, "GET /requests/stream", func(w http.ResponseWriter, r *http.Request) error {
		f, _, err := parseFilter(r.URL.Query())
		if err != nil {
			return err
		}
		return streamRequests(w, r, log, f)
	})
	route(mux, "GET /stats", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, log.Stats())
	})
	route(mux, "DELETE /log", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, api.PurgeCount{Deleted: log.Clear()})
	})
	route(mux, "POST /stats/reset", func(w http.ResponseWriter, r *http.Request) error {
		log.ResetStats()
		return noContent(w)
	})
}

// streamRequests writes the requests matching f to the client as server-sent
// events until the client goes or the log closes.
func streamRequests(w http.ResponseWriter, r *http.Request, log *reqlog.Log, f reqlog.Filter) error {
	rc := http.NewResponseController(w)
	ch, unsub := log.Subscribe()
	defer unsub()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if rc.Flush() != nil {
		return nil
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil || rc.Flush() != nil {
				return nil
			}
		case e, open := <-ch:
			if !open {
				return nil
			}
			if !f.Match(e) {
				continue
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("event: request\ndata: " + string(b) + "\n\n")); err != nil || rc.Flush() != nil {
				return nil
			}
		}
	}
}
