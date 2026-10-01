package adt

import "context"

// --- Per-object package-check marker (issue #91) ---
//
// Of the three steps in checkMutation only step 2 — the package ownership
// check — ever leaves the process: for an existing object it resolves the
// package through SearchObject, an explicitly *stateless* ADT request.
//
// A lock handle from `?_action=LOCK` is bound to the stateful ADT session it
// was issued in. A stateless request retires that session server-side, so a
// package lookup performed between the LOCK and the write that consumes the
// handle kills the handle, and the write comes back
// 423 ExceptionResourceInvalidLockHandle.
//
// Outer workflows that have already resolved and approved the package of a
// specific object mark the context. checkMutation then skips *only* the
// networked lookup, and *only* for that object. Steps 1 and 3 (operation type
// and transportable-edit policy) are pure config predicates that issue no
// request, so they always run — skipping them, as PR #125 did, would turn a
// session fix into a --read-only / --disallowed-ops /
// --allow-transportable-edits bypass.

// mutationPackageCheckedKey is the context key under which the set of
// already-approved object keys is carried.
type mutationPackageCheckedKey struct{}

// mutationPackageKey is the canonical identity used to match a marked object
// against the object an inner mutator is about to touch. It is the same
// normalisation getObjectPackage resolves against, so a class, its
// /source/main and its /includes/<part> all collapse to one key — which is
// correct, because ADT resolves the package of an include from its parent.
func mutationPackageKey(objectURL string) string {
	if objectURL == "" {
		return ""
	}
	return canonicalizeObjectURL(objectURL)
}

// withMutationPackageChecked records that objectURL's package has been
// resolved and accepted by the configured whitelist in this call. The mark is
// per-object on purpose: a blanket "the gate ran" flag would let a workflow
// that checked object A delegate an unchecked mutation of object B.
func withMutationPackageChecked(ctx context.Context, objectURL string) context.Context {
	key := mutationPackageKey(objectURL)
	if key == "" {
		return ctx
	}

	prev, _ := ctx.Value(mutationPackageCheckedKey{}).(map[string]struct{})
	if _, ok := prev[key]; ok {
		return ctx
	}

	// Copy rather than mutate: the parent context may be shared with a
	// sibling goroutine, and a marker must never travel upwards.
	next := make(map[string]struct{}, len(prev)+1)
	for k := range prev {
		next[k] = struct{}{}
	}
	next[key] = struct{}{}
	return context.WithValue(ctx, mutationPackageCheckedKey{}, next)
}

// mutationPackageAlreadyChecked reports whether this exact object was marked
// by an outer gate in the same call.
func mutationPackageAlreadyChecked(ctx context.Context, objectURL string) bool {
	key := mutationPackageKey(objectURL)
	if key == "" {
		return false
	}
	marked, _ := ctx.Value(mutationPackageCheckedKey{}).(map[string]struct{})
	_, ok := marked[key]
	return ok
}

// gateAndMark runs the full mutation gate for m and, on success, returns a
// context in which the networked package lookup for m.ObjectURL will not be
// repeated. Call it *above* the LOCK; pass the returned context to everything
// inside the lock window.
func (c *Client) gateAndMark(ctx context.Context, m MutationContext) (context.Context, error) {
	if err := c.checkMutation(ctx, m); err != nil {
		return ctx, err
	}
	return withMutationPackageChecked(ctx, m.ObjectURL), nil
}

// PrepareSourceUpdate is the exported form of gateAndMark, for callers outside
// this package (the MCP deploy handlers) that drive LOCK -> UpdateSource ->
// UNLOCK by hand. It runs the same gate UpdateSource itself would run, before
// the lock is taken, and returns a context that lets the write under the lock
// skip the redundant — and session-fatal — package lookup. The returned
// context must be used for the whole lock window.
func (c *Client) PrepareSourceUpdate(ctx context.Context, objectURL, transport string) (context.Context, error) {
	return c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateSource",
		ObjectURL: objectURL,
		Transport: transport,
	})
}

// PrepareDelete is PrepareSourceUpdate for DeleteObject: it runs the gate
// DeleteObject itself would run, before the lock is taken, and returns a
// context in which the DELETE does not resolve the package again while holding
// the lock (issue #238). The returned context must be used for the LOCK and the
// DELETE alike.
func (c *Client) PrepareDelete(ctx context.Context, objectURL, transport string) (context.Context, error) {
	return c.gateAndMark(ctx, MutationContext{
		Op:        OpDelete,
		OpName:    "DeleteObject",
		ObjectURL: objectURL,
		Transport: transport,
	})
}
