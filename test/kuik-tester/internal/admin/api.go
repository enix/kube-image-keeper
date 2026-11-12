package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

const maxBody = 8 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"encoding failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeErr maps an error to the API error contract.
func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	_, isInvalid := errors.AsType[*ValidationError](err)
	_, tooLarge := errors.AsType[*http.MaxBytesError](err)
	_, isBackend := errors.AsType[*backendError](err)
	switch {
	case isInvalid:
		status = http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case tooLarge:
		status = http.StatusRequestEntityTooLarge
	case isBackend:
		status = http.StatusBadGateway
	}
	writeJSON(w, status, api.ErrorResponse{Error: err.Error()})
}

// decode reads one JSON value, refusing unknown fields and trailing data.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T
	return decodeInto(w, r, v)
}

// decodeInto is decode over a value that holds the defaults of absent fields.
func decodeInto[T any](w http.ResponseWriter, r *http.Request, v T) (T, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return v, err
		}
		return v, invalid("invalid JSON body: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return v, err
		}
		return v, invalid("invalid JSON body: unexpected data after the value")
	}
	return v, nil
}

type handler func(w http.ResponseWriter, r *http.Request) error

func route(mux *http.ServeMux, pattern string, h handler) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeErr(w, err)
		}
	})
}

func noContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

// action registers a POST with a JSON body that answers 204.
func action[T any](mux *http.ServeMux, pattern string, fn func(r *http.Request, in T) error) {
	route(mux, pattern, func(w http.ResponseWriter, r *http.Request) error {
		in, err := decode[T](w, r)
		if err != nil {
			return err
		}
		if err := fn(r, in); err != nil {
			return err
		}
		return noContent(w)
	})
}

// idRoutes registers GET, POST, PUT /{id} and DELETE /{id} of an ID-keyed
// collection. def is the value the body decodes onto (absent fields keep it);
// view maps the stored element to the answer of a POST or PUT, nil meaning
// the element itself.
func idRoutes[T any](mux *http.ServeMux, base string, s *Service, c coll[T], def T, view func(T) any) {
	answer := func(v T) any {
		if view == nil {
			return v
		}
		return view(v)
	}
	route(mux, "GET "+base, func(w http.ResponseWriter, r *http.Request) error { return ok(w, c.list(s)) })
	route(mux, "POST "+base, func(w http.ResponseWriter, r *http.Request) error {
		v, err := decodeInto(w, r, def)
		if err != nil {
			return err
		}
		if v, err = c.create(s, v); err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, answer(v))
		return nil
	})
	route(mux, "PUT "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
		v, err := decodeInto(w, r, def)
		if err != nil {
			return err
		}
		*c.Key(&v) = r.PathValue("id")
		if v, err = c.replace(s, v); err != nil {
			return err
		}
		return ok(w, answer(v))
	})
	route(mux, "DELETE "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) error {
		if err := c.remove(s, r.PathValue("id")); err != nil {
			return err
		}
		return noContent(w)
	})
}

// nameRoutes registers GET on the collection, PUT /{param} and DELETE
// /{param} of a name-keyed collection. The path key wins over the body; PUT
// answers the stored element.
func nameRoutes[T any](mux *http.ServeMux, base, param string, s *Service, c coll[T]) {
	route(mux, "GET "+base, func(w http.ResponseWriter, r *http.Request) error { return ok(w, c.list(s)) })
	route(mux, "PUT "+base+"/{"+param+"}", func(w http.ResponseWriter, r *http.Request) error {
		v, err := decode[T](w, r)
		if err != nil {
			return err
		}
		*c.Key(&v) = r.PathValue(param)
		if v, err = c.put(s, v); err != nil {
			return err
		}
		return ok(w, v)
	})
	route(mux, "DELETE "+base+"/{"+param+"}", func(w http.ResponseWriter, r *http.Request) error {
		if err := c.remove(s, r.PathValue(param)); err != nil {
			return err
		}
		return noContent(w)
	})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// NewAPI returns the JSON API; mount it under /api/v1 with http.StripPrefix.
func NewAPI(svc *Service, log *reqlog.Log) http.Handler {
	mux := http.NewServeMux()
	stateRoutes(mux, svc)
	ruleRoutes(mux, svc)
	settingsRoutes(mux, svc)
	purgeRoutes(mux, svc, log)
	imageRoutes(mux, svc)
	destRoutes(mux, svc)
	requestRoutes(mux, log)
	return mux
}

// SameOrigin refuses, with 403, a non-GET/HEAD request whose Origin host
// differs from its Host: the admin has no authentication of its own, so this
// stops cross-site form posts. A request without Origin is allowed.
func SameOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeJSON(w, http.StatusForbidden, api.ErrorResponse{Error: fmt.Sprintf("cross-origin request refused (Origin %q)", origin)})
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}
