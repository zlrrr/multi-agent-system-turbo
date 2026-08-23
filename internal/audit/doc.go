// Package audit holds structural tests that assert repository-wide invariants
// which no single package can enforce on its own: that reasoning code performs
// no I/O of its own, that every external effect reaches the outside world
// through the guarded tool path, and that no shell is ever invoked.
//
// Governs: specs/001-mvp-core/design-lld.md §2.6 (tests), design-hld.md §7.3
package audit
