package graph

// IsContentNode reports whether n is a CONTENT section node — a KindDoc
// chunk tagged data_class="content" (text / pdf / pptx / xlsx section
// bodies). Content bodies are indexed in the dedicated content store
// (ContentSearcher), never the symbol search, and are excluded from the
// code-oriented analysis passes — so this predicate is the single place
// every package agrees on what "content" means. Markdown prose (KindDoc
// without data_class=content) and data assets (data_class="data") are NOT
// content and keep their existing treatment.
func IsContentNode(n *Node) bool {
	if n == nil || n.Kind != KindDoc || n.Meta == nil {
		return false
	}
	dc, _ := n.Meta["data_class"].(string)
	return dc == "content"
}

// NonContentNodeReader is an optional store capability: a cheap (SQL-level
// on the disk backend) enumeration of a repo's NON-content nodes, so the
// code-oriented passes (search-index build, embedding, language detection)
// never materialise a content-heavy repo's hundreds of thousands of content
// sections just to iterate past them.
type NonContentNodeReader interface {
	GetRepoNonContentNodes(repoPrefix string) []*Node
}

// RepoCodeNodes returns repoPrefix's non-content nodes. It uses the store's
// NonContentNodeReader fast path when available (the disk backend filters in
// SQL, so 525k content sections never enter memory); otherwise it falls back
// to materialising the repo's nodes and dropping content in Go — fine for the
// in-memory store, which only backs small repos. An empty repoPrefix means
// "all repos".
func RepoCodeNodes(s Store, repoPrefix string) []*Node {
	if r, ok := s.(NonContentNodeReader); ok {
		return r.GetRepoNonContentNodes(repoPrefix)
	}
	var nodes []*Node
	if repoPrefix != "" {
		nodes = s.GetRepoNodes(repoPrefix)
	}
	if len(nodes) == 0 {
		nodes = s.AllNodes()
	}
	out := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if !IsContentNode(n) {
			out = append(out, n)
		}
	}
	return out
}
