package mcp

import (
	"context"

	"github.com/zzet/gortex/internal/graph"
)

// Owner folding.
//
// Concept queries about a capability often rank several members of one type
// above the type itself: each member carries the query vocabulary and its
// own fan-in, while the owner splits the same evidence. The benchmark shape
// is a middleware/persist module whose expected answer is the option/state
// TYPES, yet the emitted symbols were the value-level members. When two or
// more top candidates share one owner type, the owner is promoted directly
// ahead of its first member — members stay as evidence, nothing is evicted.
//
// Implementation-intent queries are exempt: they ask for the concrete
// members, and Q2's expansion exists to surface exactly those.

const (
	exploreOwnerFoldScan = 8
	exploreOwnerFoldMax  = 2
)

// foldMemberOwners promotes an owner type above its members when at least
// two of the scanned top candidates belong to it. The owner node is pulled
// from the candidate list when it is already present (its later occurrence
// is removed), or fetched by one bounded member_of hop otherwise.
func (s *Server) foldMemberOwners(ctx context.Context, targets []exploreTarget) []exploreTarget {
	eng := s.engineFor(ctx)
	if eng == nil || len(targets) < 2 {
		return targets
	}
	ownerOf := func(n *graph.Node) *graph.Node {
		if n == nil || (n.Kind != graph.KindMethod && n.Kind != graph.KindFunction && n.Kind != graph.KindField) {
			return nil
		}
		for _, e := range eng.GetOutEdges(n.ID) {
			if e == nil || e.Kind != graph.EdgeMemberOf {
				continue
			}
			if owner := eng.GetSymbol(e.To); owner != nil &&
				(owner.Kind == graph.KindType || owner.Kind == graph.KindInterface) {
				return owner
			}
		}
		return nil
	}

	type ownerGroup struct {
		owner       *graph.Node
		firstMember int
		members     int
	}
	groups := map[string]*ownerGroup{}
	order := make([]string, 0, exploreOwnerFoldScan)
	rankOf := map[string]int{}
	for index, t := range targets {
		if t.node != nil {
			rankOf[t.node.ID] = index
		}
		if index >= exploreOwnerFoldScan || t.node == nil {
			continue
		}
		owner := ownerOf(t.node)
		if owner == nil {
			continue
		}
		g, ok := groups[owner.ID]
		if !ok {
			g = &ownerGroup{owner: owner, firstMember: index}
			groups[owner.ID] = g
			order = append(order, owner.ID)
		}
		g.members++
	}

	folded := 0
	for _, ownerID := range order {
		if folded >= exploreOwnerFoldMax {
			break
		}
		g := groups[ownerID]
		if g.members < 2 {
			continue
		}
		if existing, present := rankOf[ownerID]; present && existing <= g.firstMember {
			continue // the owner already leads its members
		}
		// Remove a lower-ranked occurrence of the owner, then insert it
		// directly ahead of its first member.
		kept := make([]exploreTarget, 0, len(targets)+1)
		var ownerTarget exploreTarget
		found := false
		for _, t := range targets {
			if t.node != nil && t.node.ID == ownerID {
				ownerTarget = t
				found = true
				continue
			}
			kept = append(kept, t)
		}
		if !found {
			ownerTarget = exploreTarget{node: g.owner}
		}
		insertAt := g.firstMember
		if insertAt > len(kept) {
			insertAt = len(kept)
		}
		if insertAt < len(kept) {
			ownerTarget.score = kept[insertAt].score
		}
		targets = append(kept[:insertAt:insertAt], append([]exploreTarget{ownerTarget}, kept[insertAt:]...)...)
		folded++
		rankOf = map[string]int{}
		for index, t := range targets {
			if t.node != nil {
				rankOf[t.node.ID] = index
			}
		}
	}
	return targets
}
