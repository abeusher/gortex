package trigram

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Match is one line of one file that contains the query literal.
type Match struct {
	Path string `json:"path"` // forward-slash repo-relative path
	Line int    `json:"line"` // 1-based line number
	Text string `json:"text"` // the matching line
}

// Searcher is a trigram-accelerated literal code search over a fixed
// set of files. Build it once against a repo's file list, then Grep it
// repeatedly. It is safe for concurrent Grep calls.
type Searcher struct {
	root  string
	ix    *Index
	paths []string // docID -> forward-slash repo-relative path
}

// Build reads every file — forward-slash repo-relative paths under
// root — and indexes its content. A file that cannot be read is left
// unindexed (it never matches) but keeps its docID slot so the rest
// stay aligned.
func Build(root string, relPaths []string) *Searcher {
	s := &Searcher{
		root:  root,
		ix:    New(),
		paths: make([]string, len(relPaths)),
	}
	for i, rel := range relPaths {
		rel = filepath.ToSlash(rel)
		s.paths[i] = rel
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		s.ix.Add(uint32(i), content)
	}
	return s
}

// Grep returns up to limit lines, across the indexed files, that
// contain the literal query. The trigram index narrows the file set;
// each candidate file is then scanned to confirm the match and locate
// its lines. Results are ordered by file, then by line. A non-positive
// limit returns every match.
func (s *Searcher) Grep(query string, limit int) []Match {
	if query == "" {
		return nil
	}
	var matches []Match
	for _, docID := range s.ix.Candidates(query) {
		if int(docID) >= len(s.paths) {
			continue
		}
		rel := s.paths[docID]
		f, err := os.Open(filepath.Join(s.root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if strings.Contains(text, query) {
				matches = append(matches, Match{Path: rel, Line: line, Text: text})
				if limit > 0 && len(matches) >= limit {
					_ = f.Close()
					return matches
				}
			}
		}
		_ = f.Close()
	}
	return matches
}

// DocCount returns the number of indexed files.
func (s *Searcher) DocCount() int { return s.ix.DocCount() }
