package api

// TagInfo is a tag and the digest it points to.
type TagInfo struct {
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

// Repository is a repository of a destination registry with its tags
// (GET /destinations/{host}).
type Repository struct {
	Name string    `json:"name"`
	Tags []TagInfo `json:"tags"` // never nil
}

// PurgeResult is what a purge of a destination removed
// (POST /destinations/{host}/purge).
type PurgeResult struct {
	DeletedTags  int      `json:"deletedTags"`
	Repositories []string `json:"repositories"` // never nil
}

// DestDeleteTagRequest is the body of POST /destinations/{host}/delete-tag.
type DestDeleteTagRequest struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

// DestDeleteManifestRequest is the body of POST /destinations/{host}/delete-manifest.
type DestDeleteManifestRequest struct {
	Repo   string `json:"repo"`
	Digest string `json:"digest"`
}

// DestTagRequest is the body of POST /destinations/{host}/tag; Src is a tag
// or a digest.
type DestTagRequest struct {
	Repo   string `json:"repo"`
	Src    string `json:"src"`
	NewTag string `json:"newTag"`
}

// DestPushRequest is the body of POST /destinations/{host}/push.
type DestPushRequest struct {
	Repo       string `json:"repo"`
	Tag        string `json:"tag"`
	ContentKey string `json:"contentKey,omitempty"` // "" means the repo
	Generation int    `json:"generation,omitempty"` // 0 means 1
}

// DestPurgeRequest is the body of POST /destinations/{host}/purge, answered
// with a PurgeResult.
type DestPurgeRequest struct {
	Prefix string `json:"prefix"`
}
