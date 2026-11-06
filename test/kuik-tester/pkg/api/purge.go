package api

// PurgeCount is how many objects a purge removed: DELETE /{rules|aliases|
// divergents|grants|platforms|users|hostauth|quotas}, DELETE /images and
// DELETE /log.
type PurgeCount struct {
	Deleted int `json:"deleted"`
}

// PurgeAllRequest is the body of POST /purge. Backends also purges every
// repository of every proxy backend.
type PurgeAllRequest struct {
	Backends bool `json:"backends"`
}

// PurgeAllResult is what POST /purge removed: the count of each kind, and,
// when the backends were purged, one entry per distinct backend.
type PurgeAllResult struct {
	Rules      int `json:"rules"`
	Aliases    int `json:"aliases"`
	Divergents int `json:"divergents"`
	Grants     int `json:"grants"`
	Platforms  int `json:"platforms"`
	Users      int `json:"users"`
	HostAuth   int `json:"hostAuth"`
	Quotas     int `json:"quotas"`
	Images     int `json:"images"`
	Requests   int `json:"requests"`
	// Backends is empty unless the backends were purged.
	Backends []BackendPurge `json:"backends,omitempty"`
}

// BackendPurge is the purge of one proxy backend. A failed backend has an
// Error; what it deleted before failing is counted.
type BackendPurge struct {
	Backend      string   `json:"backend"`      // scheme://host
	Repositories []string `json:"repositories"` // never nil
	DeletedTags  int      `json:"deletedTags"`
	Error        string   `json:"error,omitempty"`
}
