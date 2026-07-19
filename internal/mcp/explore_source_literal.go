package mcp

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/trigram"
)

const (
	exploreSourceLiteralRecallMaxHits          = 24
	exploreSourceLiteralRecallMaxFiles         = 0
	exploreSourceLiteralRecallBudget           = 75 * time.Millisecond
	exploreSourceLiteralRecallMaxTerms         = 2
	exploreSourceLiteralRecallMaxOwnersPerTerm = 3
	exploreSourceLiteralRecallMaxFilesPerTerm  = 2
)

type exploreSourceLiteralHit struct {
	nodeID    string
	rank      int
	anchor    int
	ambiguous bool
}

type exploreSourceLiteralDiagnostic struct {
	literal        string
	rawHits        int
	mappedOwners   int
	retainedOwners int
	retainedFiles  int
	reason         string
}

type exploreSourceLiteralRecall struct {
	hits        []exploreSourceLiteralHit
	ambiguous   bool
	ownerFiles  map[string]string
	diagnostics []exploreSourceLiteralDiagnostic
}

type exploreSourceLiteralSearch struct {
	matches          []trigram.Match
	incomplete       bool
	backend          string
	owned            bool
	lookupRepoPrefix string
}

// explorePreferredSourceLiteral picks one deterministic source-search key.
// Quoted terms have already passed the noise filter. Compact alphabetic values
// (locale/protocol/config keys) are reserved before longer prose because their
// registration sites are otherwise invisible to symbol metadata. A saturated
// compact lookup may fall back to the longest remaining term inside the same
// fixed end-to-end deadline.
func explorePreferredSourceLiteral(terms []string) string {
	best := ""
	bestLen := 0
	bestCompact := false
	for _, term := range terms {
		n := utf8.RuneCountInString(term)
		compact := exploreCompactSourceLiteral(term, n)
		if best == "" || compact && !bestCompact || compact == bestCompact && n > bestLen {
			best, bestLen, bestCompact = term, n, compact
		}
	}
	return best
}

func exploreCompactSourceLiteral(term string, runeCount int) bool {
	// Reserve priority only for two-letter alphabetic codes. Three- and
	// four-letter lowercase values are indistinguishable from common prose
	// ("test", "file", "true") and must compete by information length. They
	// remain searchable when they are the only quoted term.
	if runeCount != 2 {
		return false
	}
	lower := strings.ToLower(term)
	if _, stop := assistStopWords[lower]; stop {
		return false
	}
	switch lower {
	case "am", "an", "as", "at", "be", "by", "do", "go", "he", "hi", "if", "in", "is", "it", "me", "my", "no", "of", "oh", "ok", "on", "or", "so", "to", "up", "us", "we":
		return false
	}
	for _, r := range term {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func exploreSourceLiteralFallback(terms []string, primary string) string {
	best := ""
	bestLen := 0
	for _, term := range terms {
		if strings.EqualFold(term, primary) {
			continue
		}
		if n := utf8.RuneCountInString(term); n > bestLen {
			best, bestLen = term, n
		}
	}
	return best
}

func exploreCompactSourceLiteralTerms(terms []string) []string {
	out := make([]string, 0, min(len(terms), exploreSourceLiteralRecallMaxTerms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		if !exploreCompactSourceLiteral(term, utf8.RuneCountInString(term)) {
			continue
		}
		key := strings.ToLower(term)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if len(out) < exploreSourceLiteralRecallMaxTerms {
			out = append(out, term)
		}
	}
	return out
}

// retainExploreSourceLiteralOwners keeps file diversity before filling the
// remaining declaration slots. This prevents the first file's sibling methods
// from evicting an equally exact owner in a second file while retaining fixed
// per-term work and response bounds.
func retainExploreSourceLiteralOwners(recall exploreSourceLiteralRecall) (hits []exploreSourceLiteralHit, files int, reason string) {
	if len(recall.hits) == 0 {
		return nil, 0, "no_mapped_owner"
	}
	seenOwners := make(map[string]struct{}, min(len(recall.hits), exploreSourceLiteralRecallMaxOwnersPerTerm))
	seenFiles := make(map[string]struct{}, exploreSourceLiteralRecallMaxFilesPerTerm)
	selected := make([]bool, len(recall.hits))
	add := func(index int) {
		hit := recall.hits[index]
		if _, duplicate := seenOwners[hit.nodeID]; duplicate {
			return
		}
		seenOwners[hit.nodeID] = struct{}{}
		if file := recall.ownerFiles[hit.nodeID]; file != "" {
			seenFiles[file] = struct{}{}
		}
		selected[index] = true
		hits = append(hits, hit)
	}

	// First pass: one declaration from each distinct file.
	for index, hit := range recall.hits {
		if len(hits) >= exploreSourceLiteralRecallMaxOwnersPerTerm || len(seenFiles) >= exploreSourceLiteralRecallMaxFilesPerTerm {
			break
		}
		file := recall.ownerFiles[hit.nodeID]
		if file == "" {
			continue
		}
		if _, exists := seenFiles[file]; exists {
			continue
		}
		add(index)
	}
	// Second pass: fill remaining owner slots from already-admitted files.
	for index, hit := range recall.hits {
		if len(hits) >= exploreSourceLiteralRecallMaxOwnersPerTerm {
			break
		}
		if selected[index] {
			continue
		}
		file := recall.ownerFiles[hit.nodeID]
		if file != "" {
			if _, admitted := seenFiles[file]; !admitted && len(seenFiles) >= exploreSourceLiteralRecallMaxFilesPerTerm {
				continue
			}
		}
		add(index)
	}

	if len(hits) < len(recall.hits) {
		mappedFiles := make(map[string]struct{}, len(recall.hits))
		for _, hit := range recall.hits {
			if file := recall.ownerFiles[hit.nodeID]; file != "" {
				mappedFiles[file] = struct{}{}
			}
		}
		if len(mappedFiles) > exploreSourceLiteralRecallMaxFilesPerTerm {
			reason = "file_cap"
		} else {
			reason = "owner_cap"
		}
	}
	return hits, len(seenFiles), reason
}

// gatherExploreSourceLiteralRecall reuses the bounded raw-text path behind
// search(operation:"text") only when content_fts could not produce an exact
// symbol candidates. It searches one repository and at most two compact
// literals, maps 1-based hits to their smallest enclosing declarations, and
// returns a file-diverse owner set for the caller's existing batch hydration.
func (s *Server) gatherExploreSourceLiteralRecall(
	ctx context.Context,
	terms []string,
	repoPrefix string,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	attemptTerms := exploreCompactSourceLiteralTerms(terms)
	if len(attemptTerms) == 0 {
		if primary := explorePreferredSourceLiteral(terms); primary != "" {
			attemptTerms = append(attemptTerms, primary)
		}
	}
	if len(attemptTerms) == 0 {
		return exploreSourceLiteralRecall{}
	}

	started := time.Now()
	boundedCtx, cancelBounded := context.WithTimeout(ctx, 2*exploreSourceLiteralRecallBudget)
	defer cancelBounded()
	type literalAttempt struct {
		term       string
		search     exploreSourceLiteralSearch
		recall     exploreSourceLiteralRecall
		searchErr  error
		mappingErr error
	}
	attempt := func(term string, budget time.Duration) literalAttempt {
		result := literalAttempt{term: term}
		if term == "" || boundedCtx.Err() != nil {
			return result
		}
		attemptCtx, cancelAttempt := context.WithTimeout(boundedCtx, budget)
		defer cancelAttempt()
		searchBudget := exploreSourceLiteralRecallBudget
		if budget < searchBudget {
			searchBudget = budget
		}
		// Two-anchor recall gives each discovery the full per-anchor slice.
		// Mapping still shares attemptCtx, so the two attempts remain inside the
		// fixed 150ms request budget without silently halving grep time again.
		searchCtx, cancelSearch := context.WithTimeout(attemptCtx, searchBudget)
		result.search = s.searchExploreSourceLiteral(searchCtx, term, repoPrefix, scope)
		result.searchErr = searchCtx.Err()
		cancelSearch()
		if ctx.Err() != nil {
			return result
		}
		result.recall, result.mappingErr = s.mapDiscoveredExploreSourceLiteralMatches(
			attemptCtx, term, result.search, scope, result.searchErr,
		)
		return result
	}

	aggregate := exploreSourceLiteralRecall{ownerFiles: make(map[string]string)}
	attempted := make(map[string]struct{}, len(attemptTerms)+1)
	results := make([]literalAttempt, 0, len(attemptTerms)+1)
	run := func(term string, anchor int, budget time.Duration) literalAttempt {
		result := attempt(term, budget)
		attempted[strings.ToLower(term)] = struct{}{}
		retained, retainedFiles, capReason := retainExploreSourceLiteralOwners(result.recall)
		reason := capReason
		switch {
		case result.searchErr != nil:
			reason = "search_deadline"
		case result.mappingErr != nil:
			reason = "mapping_deadline"
		case result.search.backend == "none" || !result.search.owned && len(result.search.matches) == 0:
			reason = "backend_unavailable"
		case len(result.search.matches) == 0:
			reason = "no_match"
		case len(result.recall.hits) == 0:
			reason = "no_mapped_owner"
		case reason == "" && result.search.incomplete:
			reason = "search_cap"
		}
		aggregate.diagnostics = append(aggregate.diagnostics, exploreSourceLiteralDiagnostic{
			literal:        term,
			rawHits:        len(result.search.matches),
			mappedOwners:   len(result.recall.hits),
			retainedOwners: len(retained),
			retainedFiles:  retainedFiles,
			reason:         reason,
		})
		for _, hit := range retained {
			hit.anchor = anchor
			hit.ambiguous = result.recall.ambiguous
			aggregate.hits = append(aggregate.hits, hit)
			if file := result.recall.ownerFiles[hit.nodeID]; file != "" {
				aggregate.ownerFiles[hit.nodeID] = file
			}
		}
		aggregate.ambiguous = aggregate.ambiguous || result.recall.ambiguous
		results = append(results, result)
		return result
	}

	attemptBudget := 2 * exploreSourceLiteralRecallBudget
	if len(attemptTerms) > 1 {
		attemptBudget = exploreSourceLiteralRecallBudget
	}
	for index, term := range attemptTerms {
		if boundedCtx.Err() != nil {
			break
		}
		run(term, index, attemptBudget)
	}
	// Preserve the existing selective fallback when there is only one compact
	// anchor and it produces no settled owner. Two compact anchors already spend
	// the entire bounded term budget and are aggregated directly.
	if len(attemptTerms) == 1 && len(results) == 1 && boundedCtx.Err() == nil {
		primary := results[0]
		if fallback := exploreSourceLiteralFallback(terms, primary.term); fallback != "" &&
			(len(primary.recall.hits) == 0 || primary.recall.ambiguous || primary.search.incomplete) {
			run(fallback, 1, exploreSourceLiteralRecallBudget)
		}
	}
	for _, term := range terms {
		if _, ok := attempted[strings.ToLower(term)]; ok {
			continue
		}
		reason := "not_compact"
		if exploreCompactSourceLiteral(term, utf8.RuneCountInString(term)) {
			reason = "term_cap"
			if boundedCtx.Err() != nil {
				reason = "request_deadline"
			}
		}
		aggregate.diagnostics = append(aggregate.diagnostics, exploreSourceLiteralDiagnostic{
			literal: term,
			reason:  reason,
		})
	}
	if ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}

	if s.logger != nil {
		rawHits, mappedOwners, retainedOwners := 0, 0, 0
		reasons := make(map[string]struct{})
		for _, diagnostic := range aggregate.diagnostics {
			rawHits += diagnostic.rawHits
			mappedOwners += diagnostic.mappedOwners
			retainedOwners += diagnostic.retainedOwners
			if diagnostic.reason != "" {
				reasons[diagnostic.reason] = struct{}{}
			}
		}
		reasonKeys := make([]string, 0, len(reasons))
		for reason := range reasons {
			reasonKeys = append(reasonKeys, reason)
		}
		sort.Strings(reasonKeys)
		termRunes := 0
		backend := "none"
		owned := false
		lookupRepoPrefix := ""
		firstMatchPath := ""
		contextError := ""
		if len(results) > 0 {
			termRunes = utf8.RuneCountInString(results[0].term)
			backend = results[0].search.backend
			owned = results[0].search.owned
			lookupRepoPrefix = results[0].search.lookupRepoPrefix
			if len(results[0].search.matches) > 0 {
				firstMatchPath = results[0].search.matches[0].Path
			}
			if results[0].searchErr != nil {
				contextError = "search: " + results[0].searchErr.Error()
			}
			if results[0].mappingErr != nil {
				if contextError != "" {
					contextError += "; "
				}
				contextError += "mapping: " + results[0].mappingErr.Error()
			}
		}
		fields := []zap.Field{
			zap.Int("term_runes", termRunes),
			zap.Int("attempted_terms", len(results)),
			zap.Int("diagnostic_terms", len(aggregate.diagnostics)),
			zap.String("reasons", strings.Join(reasonKeys, ",")),
			zap.String("requested_repo_prefix", repoPrefix),
			zap.String("lookup_repo_prefix", lookupRepoPrefix),
			zap.String("first_match_path", firstMatchPath),
			zap.String("backend", backend),
			zap.Bool("owned", owned),
			zap.Int("raw_matches", rawHits),
			zap.Int("mapped_symbols", mappedOwners),
			zap.Int("retained_symbols", retainedOwners),
			zap.Bool("incomplete", aggregate.ambiguous),
			zap.String("context_error", contextError),
			zap.Duration("elapsed", time.Since(started)),
		}
		if len(aggregate.hits) == 0 || aggregate.ambiguous || contextError != "" {
			s.logger.Info("mcp: explore source literal recall incomplete", fields...)
		} else {
			s.logger.Debug("mcp: explore source literal recall", fields...)
		}
	}
	return aggregate
}

func (s *Server) mapDiscoveredExploreSourceLiteralMatches(
	ctx context.Context,
	term string,
	search exploreSourceLiteralSearch,
	scope query.QueryOptions,
	discoveryErr error,
) (exploreSourceLiteralRecall, error) {
	mappingCtx, cancelMapping := context.WithTimeout(ctx, exploreSourceLiteralRecallBudget)
	recall := s.mapExploreSourceLiteralMatchesContext(mappingCtx, term, search.matches, scope)
	mappingErr := mappingCtx.Err()
	cancelMapping()
	recall.ambiguous = recall.ambiguous || search.incomplete || discoveryErr != nil || mappingErr != nil
	return recall, mappingErr
}

func (s *Server) mapExploreSourceLiteralMatches(
	term string,
	matches []trigram.Match,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	return s.mapExploreSourceLiteralMatchesContext(context.Background(), term, matches, scope)
}

func (s *Server) mapExploreSourceLiteralMatchesContext(
	ctx context.Context,
	term string,
	matches []trigram.Match,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || len(matches) == 0 || ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	saturated := len(matches) >= exploreSourceLiteralRecallMaxHits
	if len(matches) > exploreSourceLiteralRecallMaxHits {
		matches = matches[:exploreSourceLiteralRecallMaxHits]
	}

	// Multi-repo grep stamps paths with the repository prefix. During an
	// isolated single-repo session the graph can still contain unprefixed file
	// paths, so admit one exact, scope-derived alias into the same index build.
	// Exact paths remain authoritative; this is not a fuzzy suffix match and it
	// does not add another AllNodes scan.
	repoPrefix := exploreSourceLiteralSingleRepoPrefix(scope)
	exactPaths := make([]string, 0, len(matches))
	aliasPaths := make([]string, 0, len(matches))
	exactSeen := make(map[string]struct{}, len(matches))
	aliasSeen := make(map[string]struct{}, len(matches))
	aliases := make(map[string]string, len(matches))
	for _, match := range matches {
		if !exploreTextHasExactLiteral(match.Text, term) {
			continue
		}
		if _, duplicate := exactSeen[match.Path]; !duplicate {
			exactSeen[match.Path] = struct{}{}
			exactPaths = append(exactPaths, match.Path)
		}
		if alias := exploreSourceLiteralUnprefixedPath(match.Path, repoPrefix); alias != "" {
			aliases[match.Path] = alias
			if _, duplicate := aliasSeen[alias]; !duplicate {
				aliasSeen[alias] = struct{}{}
				aliasPaths = append(aliasPaths, alias)
			}
		}
	}
	sort.Strings(exactPaths)
	sort.Strings(aliasPaths)
	orderedPaths := make([]string, 0, len(exactPaths)+len(aliasPaths))
	orderedPaths = append(orderedPaths, exactPaths...)
	for _, alias := range aliasPaths {
		if _, isExact := exactSeen[alias]; !isExact {
			orderedPaths = append(orderedPaths, alias)
		}
	}
	indexes := s.buildFileSymbolIndexForOrderedPathsContext(ctx, orderedPaths)
	seen := make(map[string]struct{}, len(matches))
	hits := make([]exploreSourceLiteralHit, 0, len(matches))
	ownerFiles := make(map[string]string, len(matches))
	for rank, match := range matches {
		if !exploreTextHasExactLiteral(match.Text, term) {
			continue
		}
		index := indexes[match.Path]
		if index == nil {
			index = indexes[aliases[match.Path]]
		}
		if index == nil {
			continue
		}
		node := index.smallestEnclosing(match.Line)
		if node == nil || node.ID == "" || !scope.ScopeAllows(node) {
			continue
		}
		if _, duplicate := seen[node.ID]; duplicate {
			continue
		}
		seen[node.ID] = struct{}{}
		hits = append(hits, exploreSourceLiteralHit{nodeID: node.ID, rank: rank})
		ownerFiles[node.ID] = node.FilePath
	}
	return exploreSourceLiteralRecall{
		hits:       hits,
		ambiguous:  saturated || len(hits) > 1,
		ownerFiles: ownerFiles,
	}
}

func exploreSourceLiteralSingleRepoPrefix(scope query.QueryOptions) string {
	prefix := ""
	for candidate, allowed := range scope.RepoAllow {
		if !allowed {
			continue
		}
		candidate = strings.TrimSuffix(strings.TrimSpace(candidate), "/")
		if candidate == "" {
			continue
		}
		if prefix != "" && prefix != candidate {
			return ""
		}
		prefix = candidate
	}
	return prefix
}

func exploreSourceLiteralUnprefixedPath(path, repoPrefix string) string {
	marker := repoPrefix + "/"
	if repoPrefix == "" || !strings.HasPrefix(path, marker) || len(path) == len(marker) {
		return ""
	}
	return strings.TrimPrefix(path, marker)
}

// searchExploreSourceLiteral mirrors search_text's literal backend while
// deliberately refusing an unscoped multi-repository fan-out. The caller's
// session locality supplies repoPrefix in normal operation.
func (s *Server) searchExploreSourceLiteral(
	ctx context.Context,
	term string,
	repoPrefix string,
	scope query.QueryOptions,
) exploreSourceLiteralSearch {
	if s.multiIndexer != nil {
		if repoPrefix == "" {
			haveScopedPrefix := false
			for prefix, allowed := range scope.RepoAllow {
				if !allowed {
					continue
				}
				prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
				if haveScopedPrefix && repoPrefix != prefix {
					return exploreSourceLiteralSearch{backend: "multi-ambiguous-scope"}
				}
				repoPrefix = prefix
				haveScopedPrefix = true
			}
		}
		result := s.multiIndexer.GrepLiteralForRepoBounded(
			ctx, repoPrefix, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
		if result.Owned {
			return exploreSourceLiteralSearch{
				matches:          result.Matches,
				incomplete:       result.Incomplete,
				backend:          "multi",
				owned:            true,
				lookupRepoPrefix: result.RepoPrefix,
			}
		}
		// Once MultiIndexer owns any repository, an unresolved prefix is an
		// ownership failure rather than permission to scan the base indexer.
		// Falling through here can leak matches from a different repository.
		if result.Configured {
			return exploreSourceLiteralSearch{
				backend:          "multi-unresolved",
				lookupRepoPrefix: repoPrefix,
			}
		}
	}
	if s.indexer != nil {
		matches, incomplete := s.indexer.GrepLiteralBounded(
			ctx, term,
			exploreSourceLiteralRecallMaxHits,
			exploreSourceLiteralRecallMaxFiles,
		)
		return exploreSourceLiteralSearch{
			matches:          matches,
			incomplete:       incomplete,
			backend:          "direct",
			owned:            true,
			lookupRepoPrefix: s.indexer.RepoPrefix(),
		}
	}
	return exploreSourceLiteralSearch{backend: "none", lookupRepoPrefix: repoPrefix}
}
