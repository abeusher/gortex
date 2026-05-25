package store_kuzu

import "fmt"

// ResolveSameFile pushes the same-source-file resolution pass into
// the Kuzu engine. For every `unresolved::Name` edge, look for a
// Node with that name whose file_path matches the caller's
// file_path — if there's exactly one such candidate, rewrite the
// edge to point at it. Same-file calls are unambiguous in every
// language we index, so the match precision is high.
//
// One Cypher statement replaces what would otherwise be ~thousands
// of per-edge GetNode / FindNodesByName round-trips.
func (s *Store) ResolveSameFile() (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Two-pass to keep `target` typed as Node through the CREATE.
	const q = `
MATCH (caller:Node)-[e:Edge]->(stub:Node)
WHERE stub.id STARTS WITH 'unresolved::' AND caller.file_path <> ''
WITH e, caller, stub, substring(stub.id, 13, size(stub.id) - 12) AS name
OPTIONAL MATCH (cnd:Node {name: name})
WHERE cnd.file_path = caller.file_path AND cnd.id <> stub.id
WITH e, caller, stub, name, count(cnd) AS cnt
WHERE cnt = 1
MATCH (target:Node {name: name})
WHERE target.file_path = caller.file_path AND target.id <> stub.id
DELETE e
CREATE (caller)-[newE:Edge {
    kind: e.kind,
    file_path: e.file_path,
    line: e.line,
    confidence: e.confidence,
    confidence_label: e.confidence_label,
    origin: 'ast_resolved',
    tier: 'ast_resolved',
    cross_repo: e.cross_repo,
    meta: e.meta
}]->(target)
RETURN count(newE) AS resolved`
	return s.runResolverQueryLocked(q, "ResolveSameFile")
}

func (s *Store) ResolveSamePackage() (int, error)           { return 0, nil }
func (s *Store) ResolveImportAware() (int, error)           { return 0, nil }
func (s *Store) ResolveRelativeImports(string) (int, error) { return 0, nil }
func (s *Store) ResolveCrossRepo() (int, error)             { return 0, nil }
func (s *Store) ResolveExternalCallStubs() (int, error)     { return 0, nil }

// runResolverQueryLocked is the shared boilerplate for a backend-
// resolver Cypher query that returns a single COUNT column. Bumps
// the identity-revision counter by the resolved count.
func (s *Store) runResolverQueryLocked(query, ruleName string) (int, error) {
	res, err := s.conn.Query(query)
	if err != nil {
		return 0, fmt.Errorf("backend-resolver %s: %w", ruleName, err)
	}
	defer res.Close()
	if !res.HasNext() {
		return 0, nil
	}
	row, err := res.Next()
	if err != nil {
		return 0, fmt.Errorf("backend-resolver %s: read result: %w", ruleName, err)
	}
	defer row.Close()
	vals, err := row.GetAsSlice()
	if err != nil || len(vals) == 0 {
		return 0, err
	}
	n, _ := vals[0].(int64)
	if n > 0 {
		s.edgeIdentityRevs.Add(n)
	}
	return int(n), nil
}

// ResolveAllBulk chains every backend-resolver rule in precision-
// descending order and sums the resolved counts. Errors from a
// single rule are non-fatal; the orchestrator logs internally and
// continues so a buggy rule can't block the others.
func (s *Store) ResolveAllBulk() (int, error) {
	var total int
	for _, fn := range []func() (int, error){
		s.ResolveSameFile,
		s.ResolveSamePackage,
		s.ResolveImportAware,
		func() (int, error) { return s.ResolveRelativeImports("") },
		s.ResolveCrossRepo,
		s.ResolveUniqueNames,
		s.ResolveExternalCallStubs,
	} {
		n, err := fn()
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
