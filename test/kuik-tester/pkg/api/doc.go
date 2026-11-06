// Package api holds the wire types of the kuik-tester admin JSON API, served
// under /api/v1: the objects of the dynamic state (rules, aliases, users,
// grants, quotas...), the request and answer bodies of the action routes, the
// image rows, the request log entries and counters, and the destination
// inventories.
//
// The kuik-tester server encodes and decodes these very types, so a client
// sees exactly what the admin sends. The package has no dependency besides
// the standard library.
//
// The package is public for Go programs that drive the tester through package
// client: tests and tools. The types change with the admin of the same
// version.
package api
