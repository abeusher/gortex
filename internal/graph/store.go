package graph

import (
	"iter"
	"sync"
)

// EdgeReindex is the per-edge payload for ReindexEdges. Edge points
// at the (already mutated) Edge value the caller wants the store to
// re-bind; OldTo is the To target the edge had BEFORE the mutation,
// so the store can drop the stale in-edge index entry for OldTo
// while writing the new one for Edge.To.
type EdgeReindex struct {
	Edge  *Edge
	OldTo string
}

// EdgeProvenanceUpdate is the per-edge payload for
// SetEdgeProvenanceBatch. Edge points at the stored Edge whose
// origin should be promoted; NewOrigin is the target tier. The store
// only persists the change (and bumps EdgeIdentityRevisions) when
// NewOrigin differs from the currently stored Origin.
type EdgeProvenanceUpdate struct {
	Edge      *Edge
	NewOrigin string
}

// Store is the persistence-and-query backend the rest of gortex sees
// behind the *Graph type. The only implementation today is the
// in-memory *Graph; future implementations will include an on-disk
// embedded-DB backend (local single-binary) and a remote network
// client. The interface is the seam that lets the rest of the
// codebase be backend-agnostic.
//
// The method set deliberately mirrors *Graph's current public API so
// the codebase compiles unchanged the day this interface lands. A few
// notes on shape:
//
//   - Slice-shaped reads (AllNodes / AllEdges / FindNodesByName / …)
//     materialise their result in memory — fine for the in-memory
//     store, but disk / remote backends will want iterator-shaped
//     variants added alongside as those implementations come online.
//
//   - Memory-estimate methods (RepoMemoryEstimate /
//     AllRepoMemoryEstimates) are inherently in-memory specific; disk
//     and remote backends return whatever they can compute and callers
//     treat the result as advisory.
//
//   - ResolveMutex() returns a backend-owned mutex that resolver
//     instances (cross-repo, temporal, external) share to serialise
//     their edge-mutation passes against each other and against the
//     indexer's incremental rewrites. Every backend needs equivalent
//     coordination; the in-memory store uses its existing
//     graph-wide resolveMu, disk backends keep a dedicated mutex
//     alongside their own write serialisation. The returned pointer
//     is owned by the store and must not be Unlocked when not held.
type Store interface {
	// --- Writes -----------------------------------------------------

	AddNode(n *Node)
	AddBatch(nodes []*Node, edges []*Edge)
	AddEdge(e *Edge)
	SetEdgeProvenance(e *Edge, newOrigin string) bool
	ReindexEdge(e *Edge, oldTo string)
	// Batched siblings of the per-edge mutators. Same semantics, but
	// disk backends amortise the per-call transaction overhead by
	// committing in implementation-chosen chunks (the in-memory
	// backend just loops). The resolver fans out per-edge mutations
	// across thousands of edges in a single ResolveAll pass, so the
	// per-call form was unusable on disk backends without these.
	// Callers MUST first mutate the *Edge fields they want persisted
	// (To / Kind / Origin / …) before handing the entry over — these
	// methods read the post-mutation Edge state and update the
	// backend's indexes accordingly.
	ReindexEdges(batch []EdgeReindex)
	SetEdgeProvenanceBatch(batch []EdgeProvenanceUpdate) (changed int)
	RemoveEdge(from, to string, kind EdgeKind) bool
	EvictFile(filePath string) (nodesRemoved, edgesRemoved int)
	EvictRepo(repoPrefix string) (nodesRemoved, edgesRemoved int)

	// --- Point lookups ---------------------------------------------

	GetNode(id string) *Node
	GetNodeByQualName(qualName string) *Node

	// --- Name + scope queries --------------------------------------

	FindNodesByName(name string) []*Node
	FindNodesByNameInRepo(name, repoPrefix string) []*Node
	GetFileNodes(filePath string) []*Node
	GetRepoNodes(repoPrefix string) []*Node

	// --- Edge adjacency --------------------------------------------

	GetOutEdges(nodeID string) []*Edge
	GetInEdges(nodeID string) []*Edge

	// --- Bulk reads ------------------------------------------------

	AllNodes() []*Node
	AllEdges() []*Edge

	// --- Predicate-shaped reads (push filters into the store) ------
	//
	// These methods replace the pre-Store idiom of `for _, e := range
	// AllEdges() { if cond { ... } }`. On the in-memory backend they
	// iterate the existing internal byKind / byPrefix buckets — same
	// algorithmic cost as the inline filter. On disk backends they
	// fan out to dedicated indexes (idx_edge_kind / idx_node_kind /
	// the to_id LIKE prefix scan, etc.) so the row count actually
	// materialised is proportional to the predicate match, not the
	// whole table.
	//
	// The resolver alone calls AllEdges/AllNodes 34× per pass and
	// throws away >99% of each scan; using these predicate methods
	// instead cut a 503-second sqlite resolver pass on a 122k-node
	// graph down to seconds.
	//
	// Iterators stop when the consumer's yield returns false.
	// Implementations MUST honour early-stop so callers can break
	// out of a search.

	// EdgesByKind yields every edge whose Kind matches.
	EdgesByKind(kind EdgeKind) iter.Seq[*Edge]

	// NodesByKind yields every node whose Kind matches.
	NodesByKind(kind NodeKind) iter.Seq[*Node]

	// EdgesWithUnresolvedTarget yields every edge whose To has the
	// "unresolved::" prefix. The resolver's main loop calls this
	// once per pass; on disk backends it should range-scan a
	// to-keyed index over the single contiguous "unresolved::" slice
	// rather than materialise the whole edges table.
	EdgesWithUnresolvedTarget() iter.Seq[*Edge]

	// --- Counts and stats ------------------------------------------

	NodeCount() int
	EdgeCount() int
	Stats() GraphStats
	RepoStats() map[string]GraphStats
	RepoPrefixes() []string

	// --- Provenance verification -----------------------------------

	EdgeIdentityRevisions() int
	VerifyEdgeIdentities() error

	// --- Memory estimation (advisory; in-memory-specific) ----------

	RepoMemoryEstimate(repoPrefix string) RepoMemoryEstimate
	AllRepoMemoryEstimates() map[string]RepoMemoryEstimate

	// --- Coordination ----------------------------------------------

	// ResolveMutex returns a backend-owned mutex resolver instances
	// share to serialise edge-mutation passes. See the package doc
	// above for the full contract.
	ResolveMutex() *sync.Mutex
}

// Compile-time assertion: *Graph satisfies the Store interface. If a
// *Graph method's signature ever drifts from the interface, the build
// fails fast here instead of at runtime when a different Store
// implementation gets swapped in.
var _ Store = (*Graph)(nil)
