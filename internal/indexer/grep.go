package indexer

import (
	"sort"

	"github.com/zzet/gortex/internal/search/trigram"
)

// GrepText runs a trigram-accelerated literal search for query across
// the indexed repo, returning up to limit matching lines (a
// non-positive limit returns every match). The trigram index is built
// lazily on first use and reused across calls; it is rebuilt only when
// a full or incremental index has advanced the repo generation, so a
// burst of searches between reindexes all hit a warm index.
func (idx *Indexer) GrepText(query string, limit int) []trigram.Match {
	if query == "" {
		return nil
	}
	s := idx.warmTrigramSearcher()
	if s == nil {
		return nil
	}
	return s.Grep(query, limit)
}

// warmTrigramSearcher returns the current trigram searcher, rebuilding it
// when the index generation has moved since the cached searcher was
// built. Returns nil before anything has been indexed.
func (idx *Indexer) warmTrigramSearcher() *trigram.Searcher {
	gen := idx.indexGen.Load()

	idx.trigramMu.Lock()
	defer idx.trigramMu.Unlock()
	if idx.trigramSearcher != nil && idx.trigramGen == gen {
		return idx.trigramSearcher
	}

	root := idx.rootPath
	if root == "" {
		return idx.trigramSearcher
	}

	idx.mtimeMu.RLock()
	rels := make([]string, 0, len(idx.fileMtimes))
	for rel := range idx.fileMtimes {
		rels = append(rels, rel)
	}
	idx.mtimeMu.RUnlock()
	sort.Strings(rels)

	idx.trigramSearcher = trigram.Build(root, rels)
	idx.trigramGen = gen
	return idx.trigramSearcher
}
