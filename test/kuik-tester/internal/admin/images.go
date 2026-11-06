package admin

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/match"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/ref"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/rules"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/state"
	"github.com/enix/kube-image-keeper/test/kuik-tester/internal/synth"
	"github.com/enix/kube-image-keeper/test/kuik-tester/pkg/api"
)

// ImageFilter selects the rows of Images: Host and Repo are substrings ("" matches
// all), Limit caps the rows built (0: no cap).
type ImageFilter struct {
	Host, Repo string
	Limit      int
}

// Images lists the seen images, the ones with a generation and the ones a
// rule names literally, sorted by host, repo and tag. It keeps the images the
// filter matches, returns how many matched and builds only the first Limit
// rows.
func (s *Service) Images(f ImageFilter) (rows []api.ImageRow, total int) {
	st := s.store.Snapshot()
	set := map[string]bool{}
	for k := range st.Seen {
		set[k] = true
	}
	for k := range st.Generations {
		set[k] = true
	}
	for _, r := range st.Rules {
		t := r.Target
		if match.Literal(t.Host) && match.Literal(t.Repo) && match.Literal(t.Tag) {
			set[state.RefKey(t.Host, t.Repo, t.Tag)] = true
		}
	}
	type image struct{ host, repo, tag string }
	images := make([]image, 0, len(set))
	for k := range set {
		h, r, t := state.SplitRefKey(k)
		if strings.Contains(h, f.Host) && strings.Contains(r, f.Repo) {
			images = append(images, image{h, r, t})
		}
	}
	slices.SortFunc(images, func(a, b image) int {
		return cmp.Or(cmp.Compare(a.host, b.host), cmp.Compare(a.repo, b.repo), cmp.Compare(a.tag, b.tag))
	})
	total = len(images)
	if f.Limit > 0 {
		images = images[:min(total, f.Limit)]
	}
	rows = make([]api.ImageRow, 0, len(images))
	for _, im := range images {
		rows = append(rows, s.row(st, im.host, im.repo, im.tag))
	}
	return rows, total
}

func (s *Service) isProxy(host string) bool {
	_, ok := s.proxies.Lookup(host)
	return ok
}

func (s *Service) row(st *state.State, host, repo, tag string) api.ImageRow {
	row := api.ImageRow{Host: host, Repo: repo, Tag: tag, Generation: st.Generation(host, repo, tag)}
	rule := rules.Evaluate(st, rules.Request{Host: host, Repo: repo, Tag: tag, Kind: api.KindManifestByTag, Method: "HEAD"})
	row.Behaviour = behaviour(rule)
	if rule != nil {
		row.RuleID = rule.ID
		row.Exact = isExact(rule.Target, host, repo, tag)
	}
	if !s.isProxy(host) {
		if idx, err := synth.Build(rules.ContentKey(st, host, repo), row.Generation, rules.Platforms(st, host, repo)); err == nil {
			d, _ := idx.Digest()
			row.IndexDigest = d.String()
			row.Pinned = s.pinned(host, repo, tag, row.IndexDigest)
		}
	}
	return row
}

func (s *Service) pinned(host, repo, tag, digest string) string {
	return host + "." + s.cfg.BaseDomain + "/" + repo + ":" + tag + "@" + digest
}

// isExact reports a target on exactly host/repo:tag with nothing else.
func isExact(t api.Target, host, repo, tag string) bool {
	return t.Host == host && t.Repo == repo && t.Tag == tag &&
		len(t.Kinds) == 0 && len(t.Methods) == 0 && t.Caller == ""
}

// behaviour summarizes the effects of a rule in application order.
func behaviour(r *api.Rule) string {
	if r == nil {
		return "ok"
	}
	var parts []string
	for _, n := range slices.Sorted(maps.Keys(r.Effects.Headers)) {
		parts = append(parts, n+": "+r.Effects.Headers[n])
	}
	if r.Effects.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("latency %dms", r.Effects.LatencyMS))
	}
	if r.Effects.Reset {
		parts = append(parts, "reset")
	}
	if r.Effects.Status != 0 {
		parts = append(parts, strconv.Itoa(r.Effects.Status))
	}
	if r.Effects.Quota != "" {
		parts = append(parts, "quota "+r.Effects.Quota)
	}
	if r.Effects.Pass {
		parts = append(parts, "pass")
	}
	return strings.Join(parts, ", ")
}

func checkHost(host string) error {
	if !ref.ValidLabel(host) {
		return invalid("host %q must be a lower-case single label of at most 63 characters", host)
	}
	return nil
}

// checkRef validates a host label, repo and tag given as a literal image.
func checkRef(host, repo, tag string) error {
	if err := checkHost(host); err != nil {
		return err
	}
	return checkRepoTag(repo, tag)
}

func checkRepoTag(repo, tag string) error {
	if !ref.ValidRepo(repo) || !ref.ValidTag(tag) {
		return invalid("invalid repository %q or tag %q", repo, tag)
	}
	return nil
}

const actionDrift = "drift"

// imageAction is one quick action of the images page.
type imageAction struct {
	Value, Label, Title string
	Effects             *api.Effects // rule inserted on top; nil for ok and drift
	when                func(api.ImageRow) bool
}

// Shows reports whether the page offers the action on row.
func (a imageAction) Shows(row api.ImageRow) bool {
	return a.when == nil || a.when(row)
}

// imageActions lists the quick actions in the order the page shows them.
var imageActions = []imageAction{
	{Value: "ok", Label: "OK", Title: "Remove the rules that target exactly this image"},
	{Value: "404", Label: "404", Title: "Answer 404 with the OCI error of the request kind", Effects: &api.Effects{Status: 404}},
	{Value: "401", Label: "401", Title: "Answer 401 UNAUTHORIZED with the host's challenge", Effects: &api.Effects{Status: 401}},
	{Value: "403", Label: "403", Title: "Answer 403 DENIED", Effects: &api.Effects{Status: 403}},
	{Value: "429", Label: "429", Title: "Answer 429 TOOMANYREQUESTS", Effects: &api.Effects{Status: 429}},
	{Value: "ratelimit0", Label: "RateLimit 0", Title: "Add RateLimit-Remaining: 0;w=21600 and serve normally",
		Effects: &api.Effects{Headers: map[string]string{"RateLimit-Remaining": "0;w=21600"}}},
	{Value: "503", Label: "503", Title: "Answer 503 (OCI error UNKNOWN)", Effects: &api.Effects{Status: 503}},
	{Value: "latency", Label: "Latency 3 s", Title: "Wait 3 s before the first byte, then serve normally", Effects: &api.Effects{LatencyMS: 3000}},
	{Value: "reset", Label: "Reset", Title: "Close the connection with a TCP RST: the client sees ECONNRESET", Effects: &api.Effects{Reset: true}},
	{Value: actionDrift, Label: "Drift", Title: "Move the tag to new content: generation + 1, new digest",
		when: func(row api.ImageRow) bool { return row.IndexDigest != "" }},
	{Value: "exempt", Label: "Exempt", Title: "Insert a pass rule for exactly this image above the broader rule that matches it",
		Effects: &api.Effects{Pass: true}, when: func(row api.ImageRow) bool { return !row.Exact && row.RuleID != "" }},
}

// ImageAction applies a quick action to host/repo:tag and returns the new
// row. Inserting actions first delete the exact rules of the image.
func (s *Service) ImageAction(host, repo, tag, action string) (api.ImageRow, error) {
	if err := checkRef(host, repo, tag); err != nil {
		return api.ImageRow{}, err
	}
	i := slices.IndexFunc(imageActions, func(a imageAction) bool { return a.Value == action })
	if i < 0 {
		return api.ImageRow{}, invalid("unknown action %q", action)
	}
	eff := imageActions[i].Effects
	if action == actionDrift && s.isProxy(host) {
		return api.ImageRow{}, invalid("drift does not apply to proxy host %q", host)
	}
	var row api.ImageRow
	err := s.update(func(st *state.State) error {
		if action == actionDrift {
			st.Drift(api.Target{Host: host, Repo: repo, Tag: tag})
		} else {
			st.Rules = deleteExact(st.Rules, host, repo, tag)
		}
		if eff != nil {
			e := *eff
			e.Headers = maps.Clone(e.Headers)
			st.Rules = append([]api.Rule{{
				ID: state.NewID(), Enabled: true,
				Target:  api.Target{Host: host, Repo: repo, Tag: tag},
				Effects: e,
			}}, st.Rules...)
		}
		row = s.row(st, host, repo, tag)
		return nil
	})
	return row, err
}

func deleteExact(rs []api.Rule, host, repo, tag string) []api.Rule {
	out := make([]api.Rule, 0, len(rs))
	for _, r := range rs {
		if !isExact(r.Target, host, repo, tag) {
			out = append(out, r)
		}
	}
	return out
}

// Resolve builds the synthetic image of host/repo:tag and records it, so
// the image is seen and its digests resolve, as after a pull.
func (s *Service) Resolve(host, repo, tag string) (api.Resolved, error) {
	if err := checkHost(host); err != nil {
		return api.Resolved{}, err
	}
	if s.isProxy(host) {
		return api.Resolved{}, invalid("host %q is a proxy, not a synthetic registry", host)
	}
	if err := checkRepoTag(repo, tag); err != nil {
		return api.Resolved{}, err
	}
	res := api.Resolved{Host: host, Repo: repo, Tag: tag}
	var plats []v1.Platform
	s.store.View(func(st *state.State) {
		res.ContentKey = rules.ContentKey(st, host, repo)
		res.Generation = st.Generation(host, repo, tag)
		plats = rules.Platforms(st, host, repo)
	})
	idx, err := synth.Build(res.ContentKey, res.Generation, plats)
	if err != nil {
		return api.Resolved{}, err
	}
	if err := s.store.RecordImage(host, repo, tag, res.ContentKey, res.Generation, idx); err != nil {
		return api.Resolved{}, err
	}
	d, err := idx.Digest()
	if err != nil {
		return api.Resolved{}, err
	}
	res.IndexDigest = d.String()
	res.Pinned = s.pinned(host, repo, tag, res.IndexDigest)
	im, err := idx.IndexManifest()
	if err != nil {
		return api.Resolved{}, err
	}
	res.Platforms = map[string]string{}
	for _, m := range im.Manifests {
		res.Platforms[m.Platform.OS+"/"+m.Platform.Architecture] = m.Digest.String()
	}
	return res, nil
}

// Drift bumps the generation of every image matched by t; see state.State.Drift.
// It returns the changed keys, never nil.
func (s *Service) Drift(t api.Target) ([]string, error) {
	t.Host = strings.ToLower(t.Host)
	if err := validGlobs("drift target", t); err != nil {
		return nil, err
	}
	if match.Literal(t.Host) && match.Literal(t.Repo) && match.Literal(t.Tag) && s.isProxy(t.Host) {
		return nil, invalid("drift does not apply to proxy host %q", t.Host)
	}
	changed := []string{}
	err := s.update(func(st *state.State) error {
		changed = append(changed[:0], st.Drift(t)...)
		return nil
	})
	return changed, err
}
