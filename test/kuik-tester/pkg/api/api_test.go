package api

import (
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// repoFoo is a repository name the wire entries share.
const repoFoo = "acme/foo"

var _ = Describe("wire encoding", func() {
	// The wire encoding is a contract with the clients: pin it.
	DescribeTable("marshals to the pinned JSON",
		func(v any, want string) {
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal(want))
		},
		Entry("a Rule",
			Rule{ID: "r1", Enabled: true, Target: Target{Host: "up", Kinds: []Kind{KindBlob}}, Effects: Effects{Status: 503, LatencyMS: 10}},
			`{"id":"r1","enabled":true,"target":{"host":"up","kinds":["blob"]},"effects":{"status":503,"latencyMs":10}}`),
		Entry("a RuleResponse",
			RuleResponse{Rule: Rule{ID: "r1"}, Warnings: []string{}},
			`{"rule":{"id":"r1","enabled":false,"target":{},"effects":{}},"warnings":[]}`),
		Entry("a Grant",
			Grant{ID: "g", Set: "s", Host: "h", Repo: "r/**", Actions: []Action{ActionPull}, Users: []string{"u"}},
			`{"id":"g","set":"s","host":"h","repo":"r/**","actions":["pull"],"users":["u"]}`),
		Entry("a LogEntry",
			LogEntry{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Host: "up", Kind: KindManifestByTag, Status: 200, Duration: 1500 * time.Microsecond},
			`{"time":"2026-01-02T03:04:05Z","host":"up","clientIp":"","user":"","userAgent":"","caller":"","method":"","kind":"manifest-by-tag","repo":"","reference":"","status":200,"digest":"","ruleId":"","forwarded":false,"durationMs":1.5}`),
		Entry("a Repository",
			Repository{Name: repoFoo, Tags: []TagInfo{{Tag: "t1", Digest: "sha256:x"}}},
			`{"name":"acme/foo","tags":[{"tag":"t1","digest":"sha256:x"}]}`),
		Entry("a SetInfo", SetInfo{Name: "kt-base", Objects: 2}, `{"name":"kt-base","objects":2}`),
		Entry("an Alias",
			Alias{ID: "a", Set: "s", Host: "up", From: "m/x", To: "acme/x"},
			`{"id":"a","set":"s","host":"up","from":"m/x","to":"acme/x"}`),
		Entry("a Divergent",
			Divergent{ID: "d", Host: "alt1", Repo: "acme/**", Salt: "x"},
			`{"id":"d","host":"alt1","repo":"acme/**","salt":"x"}`),
		Entry("a User",
			User{Name: "bob", Set: "s", Password: "pw"},
			`{"name":"bob","set":"s","password":"pw"}`),
		Entry("a HostAuth",
			HostAuth{Host: "bear*", Scheme: SchemeBearer},
			`{"host":"bear*","scheme":"bearer"}`),
		Entry("a QuotaDef with kinds and methods",
			QuotaDef{Name: "q", Limit: 2, WindowSec: 60, Key: "ip", Kinds: []Kind{KindManifestByTag}, Methods: []string{http.MethodGet}},
			`{"name":"q","limit":2,"windowSec":60,"key":"ip","kinds":["manifest-by-tag"],"methods":["GET"]}`),
		Entry("a QuotaDef without kinds or methods",
			QuotaDef{Name: "g", Limit: 1, WindowSec: 1, Key: "global"},
			`{"name":"g","limit":1,"windowSec":1,"key":"global"}`),
		Entry("a PlatformRule",
			PlatformRule{ID: "p", Host: "up", Repo: "acme/*", Platforms: []string{"linux/amd64"}},
			`{"id":"p","host":"up","repo":"acme/*","platforms":["linux/amd64"]}`),
		Entry("a Target with every field",
			Target{Host: "up", Repo: "acme/*", Tag: "t*", Kinds: []Kind{KindBlob}, Methods: []string{http.MethodGet}, Caller: "anonymous"},
			`{"host":"up","repo":"acme/*","tag":"t*","kinds":["blob"],"methods":["GET"],"caller":"anonymous"}`),
		Entry("Effects with every field",
			Effects{Status: 429, LatencyMS: 100, Reset: true, Headers: map[string]string{"RateLimit-Remaining": "0;w=60"}, Quota: "q", Pass: true},
			`{"status":429,"latencyMs":100,"reset":true,"headers":{"RateLimit-Remaining":"0;w=60"},"quota":"q","pass":true}`),
		Entry("empty Effects", Effects{}, `{}`),
		Entry("a Stat",
			Stat{Host: "up", Kind: KindBlob, Method: http.MethodGet, Status: 200, Count: 3},
			`{"host":"up","kind":"blob","method":"GET","status":200,"count":3}`),
		Entry("a Resolved",
			Resolved{Host: "up", Repo: repoFoo, Tag: "t1", ContentKey: repoFoo, Generation: 1, IndexDigest: "sha256:i", Platforms: map[string]string{"linux/amd64": "sha256:m"}, Pinned: "up.kt.test/acme/foo:t1@sha256:i"},
			`{"host":"up","repo":"acme/foo","tag":"t1","contentKey":"acme/foo","generation":1,"indexDigest":"sha256:i","platforms":{"linux/amd64":"sha256:m"},"pinned":"up.kt.test/acme/foo:t1@sha256:i"}`),
		Entry("a PurgeResult",
			PurgeResult{DeletedTags: 2, Repositories: []string{"a", "b"}},
			`{"deletedTags":2,"repositories":["a","b"]}`),
		Entry("a PurgeAllRequest", PurgeAllRequest{Backends: true}, `{"backends":true}`),
		Entry("a PurgeAllResult without backends", PurgeAllResult{Rules: 1, Images: 2},
			`{"rules":1,"aliases":0,"divergents":0,"grants":0,"platforms":0,"users":0,"hostAuth":0,"quotas":0,"images":2,"requests":0}`),
		Entry("a PurgeAllResult with a failed backend",
			PurgeAllResult{Backends: []BackendPurge{{Backend: "http://zot:5000", Repositories: []string{}, Error: "down"}}},
			`{"rules":0,"aliases":0,"divergents":0,"grants":0,"platforms":0,"users":0,"hostAuth":0,"quotas":0,"images":0,"requests":0,"backends":[{"backend":"http://zot:5000","repositories":[],"deletedTags":0,"error":"down"}]}`),
		Entry("an ImageRow",
			ImageRow{Host: "up", Repo: repoFoo, Tag: "t1", Generation: 1, IndexDigest: "sha256:i", Pinned: "up.kt.test/acme/foo:t1@sha256:i", RuleID: "r", Behaviour: "503", Exact: true},
			`{"host":"up","repo":"acme/foo","tag":"t1","generation":1,"indexDigest":"sha256:i","pinned":"up.kt.test/acme/foo:t1@sha256:i","ruleId":"r","behaviour":"503","exact":true}`),
		Entry("a ResolveRequest", ResolveRequest{Ref: "up/acme/foo:t1"}, `{"ref":"up/acme/foo:t1"}`),
		Entry("a DriftResult", DriftResult{Changed: []string{"up/acme/foo:t1"}}, `{"changed":["up/acme/foo:t1"]}`),
		Entry("a RuleMoveRequest to the top", RuleMoveRequest{Index: 0}, `{"index":0}`),
		Entry("a RuleEnabledRequest that disables", RuleEnabledRequest{Enabled: false}, `{"enabled":false}`),
		Entry("an ImageActionRequest",
			ImageActionRequest{Host: "up", Repo: repoFoo, Tag: "t1", Action: "503"},
			`{"host":"up","repo":"acme/foo","tag":"t1","action":"503"}`),
		Entry("a DestDeleteTagRequest", DestDeleteTagRequest{Repo: repoFoo, Tag: "t1"}, `{"repo":"acme/foo","tag":"t1"}`),
		Entry("a DestDeleteManifestRequest", DestDeleteManifestRequest{Repo: repoFoo, Digest: "sha256:d"}, `{"repo":"acme/foo","digest":"sha256:d"}`),
		Entry("a DestTagRequest", DestTagRequest{Repo: repoFoo, Src: "t1", NewTag: "t2"}, `{"repo":"acme/foo","src":"t1","newTag":"t2"}`),
		Entry("a DestPushRequest without content key or generation", DestPushRequest{Repo: repoFoo, Tag: "t1"}, `{"repo":"acme/foo","tag":"t1"}`),
		Entry("a DestPushRequest with both",
			DestPushRequest{Repo: repoFoo, Tag: "t1", ContentKey: "acme/bar", Generation: 2},
			`{"repo":"acme/foo","tag":"t1","contentKey":"acme/bar","generation":2}`),
		Entry("a DestPurgeRequest with an empty prefix", DestPurgeRequest{}, `{"prefix":""}`),
		Entry("an ErrorResponse", ErrorResponse{Error: "boom"}, `{"error":"boom"}`),
	)
})

var _ = Describe("LogEntry", func() {
	// A decoded log entry carries the duration the admin wrote as durationMs.
	It("round-trips through JSON", func() {
		var e LogEntry
		Expect(json.Unmarshal([]byte(`{"host":"up","kind":"blob","durationMs":3}`), &e)).To(Succeed())
		Expect(e.Host).To(Equal("up"))
		Expect(e.Kind).To(Equal(KindBlob))
		Expect(e.Duration).To(Equal(3 * time.Millisecond))
		in := LogEntry{Host: "up", Kind: KindManifestByTag, Status: 200, Duration: 1500 * time.Microsecond}
		b, err := json.Marshal(in)
		Expect(err).NotTo(HaveOccurred())
		var out LogEntry
		Expect(json.Unmarshal(b, &out)).To(Succeed())
		Expect(out).To(Equal(in))
	})
})

var _ = Describe("valid enum values", func() {
	DescribeTable("Scheme.Valid", func(s Scheme, want bool) { Expect(s.Valid()).To(Equal(want)) },
		Entry("basic", SchemeBasic, true),
		Entry("bearer", SchemeBearer, true),
		Entry("empty", Scheme(""), false),
		Entry("other", Scheme("other"), false),
	)
	DescribeTable("QuotaKey.Valid", func(k QuotaKey, want bool) { Expect(k.Valid()).To(Equal(want)) },
		Entry("global", QuotaKeyGlobal, true),
		Entry("ip", QuotaKeyIP, true),
		Entry("user", QuotaKeyUser, true),
		Entry("empty", QuotaKey(""), false),
		Entry("other", QuotaKey("other"), false),
	)
	DescribeTable("Kind.Valid", func(k Kind, want bool) { Expect(k.Valid()).To(Equal(want)) },
		Entry("ping", KindPing, true),
		Entry("manifest-by-tag", KindManifestByTag, true),
		Entry("manifest-by-digest", KindManifestByDigest, true),
		Entry("blob", KindBlob, true),
		Entry("upload", KindUpload, true),
		Entry("manifest-put", KindManifestPut, true),
		Entry("delete", KindDelete, true),
		Entry("tags-list", KindTagsList, true),
		Entry("catalog", KindCatalog, true),
		Entry("token", KindToken, true),
		Entry("empty", Kind(""), false),
		Entry("other", Kind("other"), false),
	)
	It("AllKinds returns a copy", func() {
		want := []Kind{KindPing, KindManifestByTag, KindManifestByDigest, KindBlob, KindUpload, KindManifestPut, KindDelete, KindTagsList, KindCatalog, KindToken}
		got := AllKinds()
		got[0] = "mutated"
		Expect(AllKinds()).To(Equal(want))
	})
})
