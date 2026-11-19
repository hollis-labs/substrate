// Package kinds defines portable operational contracts shared by generators,
// validation clients and Registry. It owns schemas, value types, canonical
// identities and definition hashes, not storage, runtime identity or policy.
//
// Project/1 is RC1: optional additive fields may evolve within a version and
// RC1 may change before 1.0. After stabilization, breaking changes require a new
// schema version and a pure migration. There is no predecessor to migrate yet.
package kinds
