package fakeregistry

import (
	"net/http"
	"time"

	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// recorder captures the status and digest header of a response. It
// unwraps to the underlying writer so http.NewResponseController can
// hijack through it.
type recorder struct {
	http.ResponseWriter
	status int
	digest string
}

func (rec *recorder) WriteHeader(code int) {
	// Interim responses (103 Early Hints) are not the response.
	if rec.status == 0 && code >= 200 {
		rec.status = code
		rec.digest = rec.Header().Get("Docker-Content-Digest")
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
		rec.digest = rec.Header().Get("Docker-Content-Digest")
	}
	return rec.ResponseWriter.Write(b)
}

func (rec *recorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// logged wraps h so that every request, including those h rejects early,
// is added to the request log. h fills the entry as it learns more.
func (s *Server) logged(h func(w http.ResponseWriter, r *http.Request, e *api.LogEntry)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		ua := r.UserAgent()
		e := &api.LogEntry{
			Time:      start,
			ClientIP:  s.clientIP(r),
			UserAgent: ua,
			Caller:    callerClass(ua),
			Method:    r.Method,
		}
		defer func() {
			e.Status = rec.status
			e.Digest = rec.digest
			e.Duration = time.Since(start)
			s.log.Add(*e)
		}()
		h(rec, r, e)
	})
}
