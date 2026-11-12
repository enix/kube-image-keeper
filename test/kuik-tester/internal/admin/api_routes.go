package admin

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/reqlog"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

func stateRoutes(mux *http.ServeMux, svc *Service) {
	route(mux, "GET /state", func(w http.ResponseWriter, r *http.Request) error {
		b, err := svc.StateJSON()
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return nil
	})
	route(mux, "GET /export", func(w http.ResponseWriter, r *http.Request) error {
		var b []byte
		var err error
		if set := r.URL.Query().Get("set"); set != "" {
			b, err = svc.ExportSet(set)
		} else {
			b, err = svc.Export()
		}
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Disposition", "attachment; filename=kuik-tester-state.yaml")
		_, _ = w.Write(b)
		return nil
	})
	route(mux, "POST /import", func(w http.ResponseWriter, r *http.Request) error {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
				return err
			}
			return invalid("unreadable body: %v", err)
		}
		if set := r.URL.Query().Get("set"); set != "" {
			err = svc.ImportSet(set, b)
		} else {
			err = svc.Import(b)
		}
		if err != nil {
			return err
		}
		return noContent(w)
	})
	route(mux, "GET /sets", func(w http.ResponseWriter, r *http.Request) error { return ok(w, svc.Sets()) })
	route(mux, "DELETE /sets/{name}", func(w http.ResponseWriter, r *http.Request) error {
		if err := svc.DeleteSet(r.PathValue("name")); err != nil {
			return err
		}
		return noContent(w)
	})
}

// ruleResponse is the answer of a rule write: the rule and its warnings.
func ruleResponse(r api.Rule) any { return api.RuleResponse{Rule: r, Warnings: RuleWarnings(r)} }

func ruleRoutes(mux *http.ServeMux, svc *Service) {
	// "enabled" defaults to true.
	idRoutes(mux, "/rules", svc, ruleColl, api.Rule{Enabled: true}, ruleResponse)
	action(mux, "POST /rules/{id}/move", func(r *http.Request, in api.RuleMoveRequest) error {
		return svc.MoveRule(r.PathValue("id"), in.Index)
	})
	action(mux, "POST /rules/{id}/enabled", func(r *http.Request, in api.RuleEnabledRequest) error {
		return svc.SetRuleEnabled(r.PathValue("id"), in.Enabled)
	})
}

func settingsRoutes(mux *http.ServeMux, svc *Service) {
	idRoutes(mux, "/aliases", svc, aliasColl, api.Alias{}, nil)
	idRoutes(mux, "/divergents", svc, divergentColl, api.Divergent{}, nil)
	idRoutes(mux, "/grants", svc, grantColl, api.Grant{}, nil)
	idRoutes(mux, "/platforms", svc, platformColl, api.PlatformRule{}, nil)
	nameRoutes(mux, "/users", "name", svc, userColl)
	nameRoutes(mux, "/hostauth", "host", svc, hostAuthColl)
	nameRoutes(mux, "/quotas", "name", svc, quotaColl)
	route(mux, "GET /quotas/usage", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, svc.QuotaUsage())
	})
	route(mux, "POST /quotas/{name}/reset", func(w http.ResponseWriter, r *http.Request) error {
		if err := svc.ResetQuota(r.PathValue("name")); err != nil {
			return err
		}
		return noContent(w)
	})
}

func imageRoutes(mux *http.ServeMux, svc *Service) {
	route(mux, "POST /drift", func(w http.ResponseWriter, r *http.Request) error {
		t, err := decode[api.Target](w, r)
		if err != nil {
			return err
		}
		changed, err := svc.Drift(t)
		if err != nil {
			return err
		}
		return ok(w, api.DriftResult{Changed: changed})
	})
	// POST, not GET: resolving records the image, and a POST goes through SameOrigin.
	route(mux, "POST /resolve", func(w http.ResponseWriter, r *http.Request) error {
		in, err := decode[api.ResolveRequest](w, r)
		if err != nil {
			return err
		}
		host, repo, tag := state.SplitRefKey(in.Ref)
		if host == "" || repo == "" || tag == "" || strings.Contains(tag, "/") {
			return invalid("ref %q must look like <host>/<repo>:<tag>", in.Ref)
		}
		res, err := svc.Resolve(host, repo, tag)
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	route(mux, "GET /images", func(w http.ResponseWriter, r *http.Request) error {
		rows, _ := svc.Images(ImageFilter{})
		return ok(w, rows)
	})
	route(mux, "POST /images/action", func(w http.ResponseWriter, r *http.Request) error {
		in, err := decode[api.ImageActionRequest](w, r)
		if err != nil {
			return err
		}
		row, err := svc.ImageAction(in.Host, in.Repo, in.Tag, in.Action)
		if err != nil {
			return err
		}
		return ok(w, row)
	})
}

func destRoutes(mux *http.ServeMux, svc *Service) {
	route(mux, "GET /destinations", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, svc.Destinations())
	})
	route(mux, "GET /destinations/{host}", func(w http.ResponseWriter, r *http.Request) error {
		repos, err := svc.Inventory(r.Context(), r.PathValue("host"))
		if err != nil {
			return err
		}
		return ok(w, nonNil(repos))
	})
	action(mux, "POST /destinations/{host}/delete-tag", func(r *http.Request, in api.DestDeleteTagRequest) error {
		return svc.DestDeleteTag(r.Context(), r.PathValue("host"), in.Repo, in.Tag)
	})
	action(mux, "POST /destinations/{host}/delete-manifest", func(r *http.Request, in api.DestDeleteManifestRequest) error {
		return svc.DestDeleteManifest(r.Context(), r.PathValue("host"), in.Repo, in.Digest)
	})
	action(mux, "POST /destinations/{host}/tag", func(r *http.Request, in api.DestTagRequest) error {
		return svc.DestTag(r.Context(), r.PathValue("host"), in.Repo, in.Src, in.NewTag)
	})
	route(mux, "POST /destinations/{host}/purge", func(w http.ResponseWriter, r *http.Request) error {
		in, err := decode[api.DestPurgeRequest](w, r)
		if err != nil {
			return err
		}
		res, err := svc.DestPurge(r.Context(), r.PathValue("host"), in.Prefix)
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	route(mux, "POST /destinations/{host}/purge-all", func(w http.ResponseWriter, r *http.Request) error {
		res, err := svc.DestPurgeAll(r.Context(), r.PathValue("host"))
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	action(mux, "POST /destinations/{host}/push", func(r *http.Request, in api.DestPushRequest) error {
		return svc.DestPushSynthetic(r.Context(), r.PathValue("host"), in.Repo, in.Tag, in.ContentKey, in.Generation)
	})
}

// purgeRoutes registers POST /purge, the purge of everything, and the purge
// by type: DELETE on a collection, or on /images, answers the number of
// objects removed. DELETE /rules does not conflict with DELETE /rules/{id}:
// the patterns differ in their segments.
func purgeRoutes(mux *http.ServeMux, svc *Service, log *reqlog.Log) {
	route(mux, "POST /purge", func(w http.ResponseWriter, r *http.Request) error {
		in, err := decode[api.PurgeAllRequest](w, r)
		if err != nil {
			return err
		}
		// A failed backend is in the result, not in the status: the state is purged.
		res, err := purgeAll(r.Context(), svc, log, in.Backends)
		if err != nil {
			return err
		}
		return ok(w, res)
	})
	for _, kind := range purgeKinds {
		route(mux, "DELETE /"+kind, func(w http.ResponseWriter, r *http.Request) error {
			n, err := svc.PurgeColl(kind)
			if err != nil {
				return err
			}
			return ok(w, api.PurgeCount{Deleted: n})
		})
	}
	route(mux, "DELETE /images", func(w http.ResponseWriter, r *http.Request) error {
		n, err := svc.PurgeImages()
		if err != nil {
			return err
		}
		return ok(w, api.PurgeCount{Deleted: n})
	})
}
