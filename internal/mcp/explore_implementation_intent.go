package mcp

import (
	"context"
	"strings"
	"unicode"

	"github.com/zzet/gortex/internal/graph"
)

// Implementation-intent localization.
//
// A query asking where something is IMPLEMENTED must return the concrete
// code that changes, not only the interface that names it. Ranking cannot
// guarantee that: an interface declaration legitimately dominates the text
// and centrality signals for its own name, so the concrete members that a
// fix actually edits can fall below the envelope cut. When the query
// expresses implementation intent, strong interface/abstract seeds in the
// ranked head expand through bounded implements/overrides relations, and a
// result whose head is exclusively abstract declarations refuses to declare
// answer_ready.
//
// Every rule here is generic: intent detection is plain English vocabulary,
// expansion walks graph relations, and nothing names a repository, an
// issue, or benchmark vocabulary.

const (
	// exploreImplSeedScan bounds how deep into the ranked head seeds are
	// considered; expansion never reorders the head above this line.
	exploreImplSeedScan = 5
	// exploreImplPerSeed bounds implementors admitted per abstract seed.
	exploreImplPerSeed = 4
	// exploreImplFileReserve is the number of DISTINCT implementation files
	// the expansion reserves in the final candidate set.
	exploreImplFileReserve = 3
)

var exploreImplementationIntentTerms = []string{
	"implementation", "implementations", "implement", "implements",
	"implemented", "implementing", "implementor", "implementors",
	"implementer", "implementers", "concrete", "override", "overrides",
	"overriding", "overridden", "subclass", "subclasses",
}

// exploreImplementationIntent reports whether the task text asks for the
// implementing/concrete side of an abstraction. Deliberately a plain word
// scan: the terminal-terms shaping drops or restems exactly the vocabulary
// this must detect.
func exploreImplementationIntent(task string) bool {
	for _, field := range strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, intent := range exploreImplementationIntentTerms {
			if field == intent {
				return true
			}
		}
	}
	return false
}

// exploreAbstractSeed reports whether a ranked node is an abstract
// declaration an implementation-intent query must expand: an interface, or
// an interface member (owner resolved through member_of).
func exploreAbstractSeed(getOut func(string) []*graph.Edge, getNode func(string) *graph.Node, n *graph.Node) (owner *graph.Node, abstract bool) {
	if n == nil {
		return nil, false
	}
	if n.Kind == graph.KindInterface {
		return n, true
	}
	if n.Kind != graph.KindMethod && n.Kind != graph.KindFunction {
		return nil, false
	}
	for _, e := range getOut(n.ID) {
		if e == nil || e.Kind != graph.EdgeMemberOf {
			continue
		}
		if ownerNode := getNode(e.To); ownerNode != nil && ownerNode.Kind == graph.KindInterface {
			return ownerNode, true
		}
	}
	return nil, false
}

// expandImplementationTargets inserts concrete implementors after abstract
// seeds in the ranked head. Interface seeds admit their implementing types;
// interface-member seeds admit the same-name members of those implementors.
// The abstract seed is never evicted, admitted implementors cover at most
// exploreImplFileReserve distinct new files, and every walk is one bounded
// relation hop — no transitive traversal.
func (s *Server) expandImplementationTargets(ctx context.Context, targets []exploreTarget) []exploreTarget {
	eng := s.engineFor(ctx)
	if eng == nil || len(targets) == 0 {
		return targets
	}
	present := make(map[string]struct{}, len(targets))
	presentFiles := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.node != nil {
			present[t.node.ID] = struct{}{}
			presentFiles[t.node.FilePath] = struct{}{}
		}
	}

	newFiles := make(map[string]struct{}, exploreImplFileReserve)
	admit := func(n *graph.Node) bool {
		if n == nil || n.ID == "" {
			return false
		}
		if _, dup := present[n.ID]; dup {
			return false
		}
		if _, known := presentFiles[n.FilePath]; !known {
			if len(newFiles) >= exploreImplFileReserve {
				return false
			}
			newFiles[n.FilePath] = struct{}{}
		}
		present[n.ID] = struct{}{}
		return true
	}

	implementorsOf := func(ownerID string) []*graph.Node {
		var out []*graph.Node
		for _, e := range eng.GetInEdges(ownerID) {
			if e == nil || (e.Kind != graph.EdgeImplements && e.Kind != graph.EdgeExtends) {
				continue
			}
			if impl := eng.GetSymbol(e.From); impl != nil {
				out = append(out, impl)
				if len(out) == exploreImplPerSeed {
					break
				}
			}
		}
		return out
	}
	memberOf := func(typeID, name string) *graph.Node {
		for _, e := range eng.GetInEdges(typeID) {
			if e == nil || e.Kind != graph.EdgeMemberOf {
				continue
			}
			if member := eng.GetSymbol(e.From); member != nil && member.Name == name {
				return member
			}
		}
		return nil
	}

	expanded := make([]exploreTarget, 0, len(targets)+exploreImplPerSeed)
	for index, t := range targets {
		expanded = append(expanded, t)
		if index >= exploreImplSeedScan || t.node == nil {
			continue
		}
		owner, abstract := exploreAbstractSeed(eng.GetOutEdges, eng.GetSymbol, t.node)
		if !abstract {
			continue
		}
		memberName := ""
		if owner.ID != t.node.ID {
			memberName = t.node.Name
		}
		for _, impl := range implementorsOf(owner.ID) {
			concrete := impl
			if memberName != "" {
				if member := memberOf(impl.ID, memberName); member != nil {
					concrete = member
				}
			}
			if !admit(concrete) {
				continue
			}
			expanded = append(expanded, exploreTarget{
				node:  concrete,
				score: t.score * 0.98,
			})
		}
	}
	return expanded
}

// exploreImplementationAnswerBlocked refuses answer_ready when an
// implementation-intent query's visible head holds only abstract
// declarations: the concrete code the query asks for is not in evidence.
func exploreImplementationAnswerBlocked(task string, targets []exploreTarget, getOut func(string) []*graph.Edge, getNode func(string) *graph.Node) bool {
	if !exploreImplementationIntent(task) {
		return false
	}
	scanned := 0
	for _, t := range targets {
		if t.node == nil {
			continue
		}
		if scanned++; scanned > exploreImplSeedScan {
			break
		}
		if _, abstract := exploreAbstractSeed(getOut, getNode, t.node); !abstract {
			return false
		}
	}
	return scanned > 0
}
