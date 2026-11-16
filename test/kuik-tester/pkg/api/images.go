package api

// Resolved is a synthetic image built and recorded on demand (POST /resolve).
type Resolved struct {
	Host        string            `json:"host"`
	Repo        string            `json:"repo"`
	Tag         string            `json:"tag"`
	ContentKey  string            `json:"contentKey"`
	Generation  int               `json:"generation"`
	IndexDigest string            `json:"indexDigest"`
	Platforms   map[string]string `json:"platforms"` // "linux/amd64" → manifest digest
	Pinned      string            `json:"pinned"`
}

// ImageRow is an image the admin knows about, with what a manifest HEAD by
// tag would meet (GET /images).
type ImageRow struct {
	Host        string `json:"host"`
	Repo        string `json:"repo"`
	Tag         string `json:"tag"`
	Generation  int    `json:"generation"`
	IndexDigest string `json:"indexDigest"` // "" on proxy hosts
	// Pinned is "<host>.<baseDomain>/<repo>:<tag>@<indexDigest>", "" on proxy hosts.
	Pinned    string `json:"pinned"`
	RuleID    string `json:"ruleId"`
	Behaviour string `json:"behaviour"`
	// Exact is true when the matching rule targets exactly this image.
	Exact bool `json:"exact"`
}

// ResolveRequest is the body of POST /resolve: ref is "<host>/<repo>:<tag>".
type ResolveRequest struct {
	Ref string `json:"ref"`
}

// DriftResult is the answer of POST /drift (body: Target): the changed
// references, never nil.
type DriftResult struct {
	Changed []string `json:"changed"`
}

// ImageActionRequest is the body of POST /images/action.
type ImageActionRequest struct {
	Host   string `json:"host"`
	Repo   string `json:"repo"`
	Tag    string `json:"tag"`
	Action string `json:"action"`
}
