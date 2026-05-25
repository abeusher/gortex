package store_duckdb

import "fmt"

// ResolveSameFile pushes the same-source-file resolution pass into
// DuckDB as a single UPDATE...FROM. For every edge whose to_id is
// `unresolved::Name`, if exactly one Node with that name shares
// the caller's file_path, rewrite to_id in place and promote
// origin/tier to ast_resolved.
func (s *Store) ResolveSameFile() (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	const q = `
WITH unique_candidates AS (
    SELECT e.edge_id, MIN(t.id) AS target_id
    FROM edges e
    JOIN nodes c ON c.id = e.from_id
    JOIN nodes t ON t.name = substring(e.to_id, 13)
                AND t.file_path = c.file_path
                AND t.id <> e.to_id
                AND c.file_path <> ''
    WHERE e.to_id LIKE 'unresolved::%'
    GROUP BY e.edge_id
    HAVING COUNT(*) = 1
)
UPDATE edges
SET to_id  = u.target_id,
    origin = 'ast_resolved',
    tier   = 'ast_resolved'
FROM unique_candidates u
WHERE edges.edge_id = u.edge_id`
	return s.runResolverUpdateLocked(q, "ResolveSameFile")
}

func (s *Store) ResolveSamePackage() (int, error)           { return 0, nil }
func (s *Store) ResolveImportAware() (int, error)           { return 0, nil }
func (s *Store) ResolveRelativeImports(string) (int, error) { return 0, nil }
func (s *Store) ResolveCrossRepo() (int, error)             { return 0, nil }
func (s *Store) ResolveExternalCallStubs() (int, error)     { return 0, nil }

// runResolverUpdateLocked is shared boilerplate for a backend-
// resolver UPDATE that returns RowsAffected. Bumps the identity-
// revision counter by the resolved count.
func (s *Store) runResolverUpdateLocked(query, ruleName string) (int, error) {
	res, err := s.db.Exec(query)
	if err != nil {
		return 0, fmt.Errorf("backend-resolver %s: %w", ruleName, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.edgeIdentityRevs.Add(n)
	}
	return int(n), nil
}

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
