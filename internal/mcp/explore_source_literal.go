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
	exploreSourceLiteralRecallMaxHits  = 24
	exploreSourceLiteralRecallMaxFiles = 0
	exploreSourceLiteralRecallBudget   = 75 * time.Millisecond
)

type exploreSourceLiteralHit struct {
	nodeID string
	rank   int
}

type exploreSourceLiteralRecall struct {
	hits      []exploreSourceLiteralHit
	ambiguous bool
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

// gatherExploreSourceLiteralRecall reuses the bounded raw-text path behind
// search(operation:"text") only when content_fts could not produce an exact
// symbol candidate. It searches one repository and one literal, maps each
// 1-based line hit to the smallest enclosing code symbol, and returns IDs for
// the caller's existing single batch graph hydration.
func (s *Server) gatherExploreSourceLiteralRecall(
	ctx context.Context,
	terms []string,
	repoPrefix string,
	scope query.QueryOptions,
) exploreSourceLiteralRecall {
	if s == nil || ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}
	primary := explorePreferredSourceLiteral(terms)
	if primary == "" {
		return exploreSourceLiteralRecall{}
	}

	// Search and symbol mapping historically had one 75 ms phase each. Keep the
	// same 150 ms end-to-end ceiling while allowing a fast, saturated compact-key
	// lookup to spend only the remaining time on one more-selective fallback.
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
	attempt := func(term string) literalAttempt {
		result := literalAttempt{term: term}
		if term == "" || boundedCtx.Err() != nil {
			return result
		}
		searchCtx, cancelSearch := context.WithTimeout(boundedCtx, exploreSourceLiteralRecallBudget)
		result.search = s.searchExploreSourceLiteral(searchCtx, term, repoPrefix, scope)
		result.searchErr = searchCtx.Err()
		cancelSearch()
		if ctx.Err() != nil {
			return result
		}
		result.recall, result.mappingErr = s.mapDiscoveredExploreSourceLiteralMatches(
			boundedCtx, term, result.search, scope, result.searchErr,
		)
		return result
	}

	chosen := attempt(primary)
	fallbackAttempted := false
	fallback := exploreSourceLiteralFallback(terms, primary)
	primaryCompact := exploreCompactSourceLiteral(primary, utf8.RuneCountInString(primary))
	if primaryCompact && fallback != "" && boundedCtx.Err() == nil &&
		(len(chosen.recall.hits) == 0 || chosen.recall.ambiguous || chosen.search.incomplete) {
		fallbackAttempted = true
		candidate := attempt(fallback)
		// Preserve a useful compact-key result unless the longer term is the only
		// mapped result or resolves the compact lookup's ambiguity.
		if len(candidate.recall.hits) > 0 &&
			(len(chosen.recall.hits) == 0 || !candidate.recall.ambiguous) {
			chosen = candidate
		}
	}
	if ctx.Err() != nil {
		return exploreSourceLiteralRecall{}
	}

	if s.logger != nil {
		contextError := ""
		if chosen.searchErr != nil {
			contextError = "search: " + chosen.searchErr.Error()
		}
		if chosen.mappingErr != nil {
			if contextError != "" {
				contextError += "; "
			}
			contextError += "mapping: " + chosen.mappingErr.Error()
		}
		firstMatchPath := ""
		if len(chosen.search.matches) > 0 {
			firstMatchPath = chosen.search.matches[0].Path
		}
		fields := []zap.Field{
			zap.Int("term_runes", utf8.RuneCountInString(chosen.term)),
			zap.Bool("fallback_attempted", fallbackAttempted),
			zap.Bool("fallback_selected", chosen.term != primary),
			zap.String("requested_repo_prefix", repoPrefix),
			zap.String("lookup_repo_prefix", chosen.search.lookupRepoPrefix),
			zap.String("first_match_path", firstMatchPath),
			zap.String("backend", chosen.search.backend),
			zap.Bool("owned", chosen.search.owned),
			zap.Int("raw_matches", len(chosen.search.matches)),
			zap.Int("mapped_symbols", len(chosen.recall.hits)),
			zap.Bool("incomplete", chosen.search.incomplete),
			zap.String("context_error", contextError),
			zap.Duration("elapsed", time.Since(started)),
		}
		if len(chosen.recall.hits) == 0 || chosen.search.incomplete || contextError != "" {
			s.logger.Info("mcp: explore source literal recall incomplete", fields...)
		} else {
			s.logger.Debug("mcp: explore source literal recall", fields...)
		}
	}
	return chosen.recall
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
	}
	return exploreSourceLiteralRecall{
		hits:      hits,
		ambiguous: saturated || len(hits) > 1,
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
