// Package state keeps, in a local file, what a migration from kuik v2 needs once v2 is gone:
// the v2 resources with their status, the origin of each image v2 rewrote, and the outcome
// of the retag.
package state
