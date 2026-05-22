package search

import "strings"

// EquivalenceTable is a deterministic, LLM-free synonym table over
// universal software-concept classes. Each class is a set of words
// that name the same idea across virtually every codebase ("auth" /
// "authentication" / "login" / "signin"; "delete" / "remove" /
// "destroy"). Expand returns a token's class siblings so the search
// layer can bridge query vocabulary to the words a symbol actually
// uses -- the deterministic complement to LLM query expansion, and
// the only expansion available when no LLM provider is configured.
//
// The table is curated and intentionally conservative: only classes
// whose members are genuinely interchangeable in code identifiers
// belong here. Domain-bearing words that mean different things in
// different codebases are left out -- a false synonym inflates the
// BM25 candidate pool with noise.
type EquivalenceTable struct {
	// member maps each lowercased word to the index of its class in
	// classes. A word in two classes keeps the first; the curated
	// table is built to avoid overlap.
	member  map[string]int
	classes [][]string
}

// curatedClasses is the compiled-in baseline. Style mirrors the
// static expansionStoplist / assistStopWords tables -- a flat literal
// kept short and reviewed by hand. Each inner slice is one class.
var curatedClasses = [][]string{
	{"auth", "authentication", "authenticate", "login", "signin", "logon", "credential", "credentials"},
	{"authz", "authorization", "authorize", "permission", "permissions", "acl", "rbac"},
	{"delete", "remove", "destroy", "drop", "erase", "purge", "unlink"},
	{"create", "add", "new", "make", "insert", "register"},
	{"update", "modify", "edit", "change", "patch", "mutate"},
	{"fetch", "get", "retrieve", "load", "read", "lookup"},
	{"save", "store", "persist", "write", "commit", "flush"},
	{"config", "configuration", "configure", "settings", "options", "preferences"},
	{"error", "err", "fault", "failure", "exception"},
	{"validate", "validation", "verify", "check", "assert", "ensure"},
	{"parse", "parser", "decode", "deserialize", "unmarshal", "unpack"},
	{"encode", "serialize", "marshal", "pack", "format"},
	{"connect", "connection", "dial", "session", "socket"},
	{"close", "disconnect", "shutdown", "teardown", "dispose"},
	{"start", "begin", "init", "initialize", "bootstrap", "launch", "boot"},
	{"stop", "halt", "cancel", "abort", "terminate", "kill"},
	{"send", "publish", "emit", "dispatch", "post", "push"},
	{"receive", "consume", "subscribe", "listen", "handle", "recv"},
	{"encrypt", "encryption", "cipher", "crypt"},
	{"decrypt", "decryption", "decipher"},
	{"cache", "caching", "memoize", "memoise"},
	{"queue", "buffer", "backlog", "pipeline"},
	{"log", "logger", "logging", "trace", "tracer"},
	{"metric", "metrics", "telemetry", "instrumentation", "stats"},
	{"retry", "retries", "backoff", "reattempt"},
	{"throttle", "ratelimit", "ratelimiter", "debounce"},
	{"user", "account", "member", "profile"},
	{"request", "req", "query"},
	{"response", "resp", "reply", "result"},
	{"token", "jwt", "bearer", "apikey"},
	{"hash", "digest", "checksum", "fingerprint"},
	{"middleware", "interceptor", "filter", "hook"},
	{"migrate", "migration", "schema"},
	{"index", "indexer", "indexing"},
	{"search", "query", "find", "lookup"},
}

// NewEquivalenceTable builds the curated table plus any repo-supplied
// extra classes. extra maps a class label to its member words; the
// label itself joins the class so a search for the label hits every
// member. Words in extra are merged into an existing class when they
// already belong to one, so a project can extend a curated class
// rather than fork it.
func NewEquivalenceTable(extra map[string][]string) *EquivalenceTable {
	t := &EquivalenceTable{member: map[string]int{}}
	for _, class := range curatedClasses {
		t.addClass(class)
	}
	for label, words := range extra {
		members := make([]string, 0, len(words)+1)
		if l := strings.ToLower(strings.TrimSpace(label)); l != "" {
			members = append(members, l)
		}
		for _, w := range words {
			if l := strings.ToLower(strings.TrimSpace(w)); l != "" {
				members = append(members, l)
			}
		}
		t.addClass(members)
	}
	return t
}

// addClass folds one class into the table. If any word already maps
// to a class, every other word in the new group is merged into that
// existing class; otherwise a fresh class is appended. Empty and
// duplicate words are dropped.
func (t *EquivalenceTable) addClass(words []string) {
	clean := make([]string, 0, len(words))
	seen := map[string]struct{}{}
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		clean = append(clean, w)
	}
	if len(clean) < 2 {
		return
	}
	// Find an existing class any word already belongs to.
	target := -1
	for _, w := range clean {
		if idx, ok := t.member[w]; ok {
			target = idx
			break
		}
	}
	if target < 0 {
		target = len(t.classes)
		t.classes = append(t.classes, nil)
	}
	for _, w := range clean {
		if _, ok := t.member[w]; ok {
			continue
		}
		t.member[w] = target
		t.classes[target] = append(t.classes[target], w)
	}
}

// Expand returns the class siblings of token -- every other word in
// its equivalence class -- or nil when the token is in no class. The
// token itself is never included. Lookup is case-insensitive.
func (t *EquivalenceTable) Expand(token string) []string {
	if t == nil {
		return nil
	}
	tok := strings.ToLower(strings.TrimSpace(token))
	if tok == "" {
		return nil
	}
	idx, ok := t.member[tok]
	if !ok {
		return nil
	}
	class := t.classes[idx]
	out := make([]string, 0, len(class)-1)
	for _, w := range class {
		if w != tok {
			out = append(out, w)
		}
	}
	return out
}

// ClassCount reports the number of equivalence classes -- curated
// plus any merged-in repo extras. Used by tests and diagnostics.
func (t *EquivalenceTable) ClassCount() int {
	if t == nil {
		return 0
	}
	return len(t.classes)
}
