package api

import "slices"

// Scheme is the auth scheme of a host (HostAuth).
type Scheme string

// The auth schemes of a host.
const (
	// SchemeBasic challenges with Basic and takes Basic credentials.
	SchemeBasic Scheme = "basic"
	// SchemeBearer needs a token from the host's /token endpoint on every request but that one.
	SchemeBearer Scheme = "bearer"
)

var schemes = []Scheme{SchemeBasic, SchemeBearer}

// Valid reports a known auth scheme.
func (s Scheme) Valid() bool { return slices.Contains(schemes, s) }

// QuotaKey is what a quota counts requests by (QuotaDef.Key).
type QuotaKey string

// The keys of a quota.
const (
	QuotaKeyGlobal QuotaKey = "global" // one counter for every request
	QuotaKeyIP     QuotaKey = "ip"     // one counter per client IP
	QuotaKeyUser   QuotaKey = "user"   // one counter per user, Anonymous for requests without credentials
)

var quotaKeys = []QuotaKey{QuotaKeyGlobal, QuotaKeyIP, QuotaKeyUser}

// Valid reports a known quota key.
func (k QuotaKey) Valid() bool { return slices.Contains(quotaKeys, k) }

// Anonymous is the caller of a request without credentials: the
// Target.Caller that matches it, and its counter key under QuotaKeyUser.
const Anonymous = "anonymous"

// Kind is the endpoint kind of a registry request.
type Kind string

// The endpoint kinds of a registry request.
const (
	KindPing             Kind = "ping"
	KindManifestByTag    Kind = "manifest-by-tag"
	KindManifestByDigest Kind = "manifest-by-digest"
	KindBlob             Kind = "blob"
	KindUpload           Kind = "upload"
	KindManifestPut      Kind = "manifest-put"
	KindDelete           Kind = "delete"
	KindTagsList         Kind = "tags-list"
	KindCatalog          Kind = "catalog"
	KindToken            Kind = "token"
)

var kinds = []Kind{KindPing, KindManifestByTag, KindManifestByDigest, KindBlob, KindUpload, KindManifestPut, KindDelete, KindTagsList, KindCatalog, KindToken}

// Valid reports a known endpoint kind.
func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// AllKinds returns every endpoint kind, in a new slice on every call.
func AllKinds() []Kind { return slices.Clone(kinds) }

// Action is what a grant allows on a repository.
type Action string

// The actions a grant may allow.
const (
	ActionPull   Action = "pull"
	ActionPush   Action = "push"
	ActionDelete Action = "delete"
)

// Every collection object may carry a Set: a name grouping the objects a
// test or a fixture created, so they can be exported or deleted together.

// Target selects requests. Empty fields match anything.
type Target struct {
	Host    string   `json:"host,omitempty"`
	Repo    string   `json:"repo,omitempty"`
	Tag     string   `json:"tag,omitempty"`
	Kinds   []Kind   `json:"kinds,omitempty"`
	Methods []string `json:"methods,omitempty"`
	// Caller is "" (any), Anonymous, or a user name.
	Caller string `json:"caller,omitempty"`
}

// Effects is what a matching rule does to a request.
type Effects struct {
	Status    int               `json:"status,omitempty"`
	LatencyMS int               `json:"latencyMs,omitempty"`
	Reset     bool              `json:"reset,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Quota     string            `json:"quota,omitempty"`
	Pass      bool              `json:"pass,omitempty"`
}

// Rule is a fault rule; the first enabled matching rule wins.
type Rule struct {
	ID      string  `json:"id"`
	Set     string  `json:"set,omitempty"`
	Enabled bool    `json:"enabled"`
	Target  Target  `json:"target"`
	Effects Effects `json:"effects"`
	Note    string  `json:"note,omitempty"`
}

// RuleResponse is the answer of a rule creation (POST /rules) or update
// (PUT /rules/{id}): the stored rule and the warnings it raises.
type RuleResponse struct {
	Rule     Rule     `json:"rule"`
	Warnings []string `json:"warnings"`
}

// RuleMoveRequest is the body of POST /rules/{id}/move; index 0 is the top.
type RuleMoveRequest struct {
	Index int `json:"index"`
}

// RuleEnabledRequest is the body of POST /rules/{id}/enabled.
type RuleEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// ErrorResponse is the body of every error the API handlers write.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Alias rewrites a repo path prefix to compute the content key.
type Alias struct {
	ID   string `json:"id"`
	Set  string `json:"set,omitempty"`
	Host string `json:"host"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Divergent salts the content key so a host serves different digests.
type Divergent struct {
	ID   string `json:"id"`
	Set  string `json:"set,omitempty"`
	Host string `json:"host"`
	Repo string `json:"repo"`
	Salt string `json:"salt"`
}

// User is a registry account.
type User struct {
	Name     string `json:"name"`
	Set      string `json:"set,omitempty"`
	Password string `json:"password"`
}

// Grant restricts actions on matching repos to the listed users.
type Grant struct {
	ID      string   `json:"id"`
	Set     string   `json:"set,omitempty"`
	Host    string   `json:"host"`
	Repo    string   `json:"repo"`
	Actions []Action `json:"actions"`
	Users   []string `json:"users"`
}

// HostAuth sets the auth scheme of a host.
type HostAuth struct {
	Host   string `json:"host"`
	Set    string `json:"set,omitempty"`
	Scheme Scheme `json:"scheme"`
}

// QuotaDef is a fixed-window request quota.
type QuotaDef struct {
	Name      string   `json:"name"`
	Set       string   `json:"set,omitempty"`
	Limit     int      `json:"limit"`
	WindowSec int      `json:"windowSec"`
	Key       QuotaKey `json:"key"`
	Kinds     []Kind   `json:"kinds,omitempty"`
	Methods   []string `json:"methods,omitempty"`
}

// PlatformRule narrows the platforms a host/repo serves; the first matching
// rule wins and no rule means every platform.
type PlatformRule struct {
	ID   string `json:"id"`
	Set  string `json:"set,omitempty"`
	Host string `json:"host"`
	Repo string `json:"repo"`
	// Platforms are "os/arch" names among the synthetic platforms, at least one.
	Platforms []string `json:"platforms"`
}

// SetInfo is a set in use and the number of objects it groups.
type SetInfo struct {
	Name    string `json:"name"`
	Objects int    `json:"objects"`
}
