package contracts

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// EndpointConstStore is the minimal graph capability ResolveEndpointArg needs
// to dereference a constant endpoint argument — a route path / queue / topic
// referenced by a const identifier — to its literal value graph-wide. Both the
// in-memory *graph.Graph and the sqlite store satisfy it. A nil store disables
// const dereference, leaving string-literal handling (and composite literals
// whose field is itself a literal) working.
type EndpointConstStore interface {
	// FindNodesByNames maps each requested bare name to the graph nodes that
	// declare it across the indexed files.
	FindNodesByNames(names []string) map[string][]*graph.Node
	// ConstantValuesByNodeIDs returns the recorded literal value for each
	// const node id; ids with no recorded value are omitted.
	ConstantValuesByNodeIDs(nodeIDs []string) (map[string]string, error)
}

// endpointCompositeFields are the struct-field names whose value carries the
// endpoint string inside an options / request composite literal — e.g. the
// QueueUrl of an &sqs.SendMessageInput{...} or the Path of a request config.
// Matched case-insensitively on the field's bare name. Kept small and explicit
// so an unrelated struct literal does not accidentally yield an endpoint.
var endpointCompositeFields = map[string]bool{
	"queueurl": true, // AWS SQS *Input
	"topicarn": true, // AWS SNS *Input
	"url":      true,
	"endpoint": true,
	"path":     true,
	"route":    true,
	"queue":    true,
	"topic":    true,
}

// ResolveEndpointArg resolves a call-argument node to a concrete endpoint
// string (route path / queue / topic) when possible, returning ("", false)
// when it can't.
//
// It handles three argument shapes:
//
//  1. a string literal — unquoted to its literal value;
//  2. an identifier (or pkg-qualified selector) referencing a Go const —
//     dereferenced graph-wide via the constant store, preferring a same-file
//     definition and otherwise requiring a repo-wide UNIQUE value; distinct
//     candidate values are ambiguous and dropped rather than guessed;
//  3. a composite literal (&Type{Field: …} / Type{Field: …}) — the value of
//     its endpoint field (see endpointCompositeFields) resolved recursively
//     (the field value may itself be a literal or a const identifier).
//
// When forRoute is true the resolved value of a const / composite dereference
// is vetted through the HTTP route guard (IsLikelyHTTPRouteLiteral); a value
// that is really a filesystem / config path is rejected. A plain string literal
// is the author's explicit intent and is never guarded, so literal handling is
// byte-identical to a direct unquote. forRoute must be false for queue / topic
// (pub/sub) contexts, where the route guard does not apply.
//
// store may be nil, which disables const dereference; paths 1 and a composite
// whose field is itself a literal still resolve.
func ResolveEndpointArg(arg *sitter.Node, src []byte, filePath, repoPrefix string, store EndpointConstStore, forRoute bool) (string, bool) {
	value, derefd, ok := resolveEndpointValue(arg, src, filePath, repoPrefix, store)
	if !ok || value == "" {
		return "", false
	}
	if forRoute && derefd && !IsLikelyHTTPRouteLiteral(value, "") {
		return "", false
	}
	return value, true
}

// resolveEndpointValue returns (value, derefd, ok). derefd is true when the
// value came from a const or composite dereference (a value the route guard
// should vet) and false for a direct string literal (explicit author intent).
func resolveEndpointValue(arg *sitter.Node, src []byte, filePath, repoPrefix string, store EndpointConstStore) (string, bool, bool) {
	if arg == nil {
		return "", false, false
	}
	switch arg.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		if v, ok := stringLiteralValue(arg, src); ok {
			return v, false, true
		}
		return "", false, false
	case "identifier":
		v, ok := resolveConstIdentifier(arg.Content(src), filePath, repoPrefix, store)
		return v, true, ok
	case "selector_expression":
		// pkg.CONST / routes.UsersPath — dereference by the trailing name.
		if f := arg.ChildByFieldName("field"); f != nil {
			v, ok := resolveConstIdentifier(f.Content(src), filePath, repoPrefix, store)
			return v, true, ok
		}
		return "", false, false
	case "unary_expression":
		// &Type{...} — resolve the pointed-to composite. The operand field
		// name is grammar-version-dependent, so fall back to the first named
		// child (the operator `&` is an anonymous child).
		op := arg.ChildByFieldName("operand")
		if op == nil {
			op = firstNamedChild(arg)
		}
		if op != nil {
			return resolveEndpointValue(op, src, filePath, repoPrefix, store)
		}
		return "", false, false
	case "parenthesized_expression":
		if inner := firstNamedChild(arg); inner != nil {
			return resolveEndpointValue(inner, src, filePath, repoPrefix, store)
		}
		return "", false, false
	case "composite_literal":
		return resolveCompositeEndpointField(arg, src, filePath, repoPrefix, store)
	}
	return "", false, false
}

// resolveCompositeEndpointField finds the endpoint field of a composite literal
// (the first field whose bare name is in endpointCompositeFields) and resolves
// its value recursively. A composite read is always a dereference, so derefd is
// reported true.
func resolveCompositeEndpointField(comp *sitter.Node, src []byte, filePath, repoPrefix string, store EndpointConstStore) (string, bool, bool) {
	body := compositeBodyNode(comp)
	if body == nil {
		return "", false, false
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		kv := body.NamedChild(i)
		if kv == nil || kv.Type() != "keyed_element" || kv.NamedChildCount() < 2 {
			continue
		}
		keyNode := unwrapLiteralElement(kv.NamedChild(0))
		valNode := unwrapLiteralElement(kv.NamedChild(1))
		if keyNode == nil || valNode == nil {
			continue
		}
		if !endpointCompositeFields[strings.ToLower(strings.TrimSpace(keyNode.Content(src)))] {
			continue
		}
		if v, _, ok := resolveEndpointValue(valNode, src, filePath, repoPrefix, store); ok && v != "" {
			return v, true, true
		}
	}
	return "", false, false
}

// resolveConstIdentifier dereferences a const identifier to its literal value
// using the graph-wide constant store. A same-file definition wins outright;
// otherwise the value must be unique across the repo's candidates — distinct
// candidate values are ambiguous and dropped (never guessed).
func resolveConstIdentifier(name, filePath, repoPrefix string, store EndpointConstStore) (string, bool) {
	if store == nil {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	cands := store.FindNodesByNames([]string{name})[name]
	if len(cands) == 0 {
		return "", false
	}
	var ids []string
	sameFileIDs := map[string]bool{}
	for _, n := range cands {
		if n == nil || n.Kind != graph.KindConstant {
			continue
		}
		if !nodeInRepoScope(n, repoPrefix) {
			continue
		}
		ids = append(ids, n.ID)
		if filePath != "" && n.FilePath == filePath {
			sameFileIDs[n.ID] = true
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	vals, err := store.ConstantValuesByNodeIDs(ids)
	if err != nil || len(vals) == 0 {
		return "", false
	}
	// A same-file definition wins outright.
	if v, ok := uniqueEndpointValue(vals, sameFileIDs); ok {
		return v, true
	}
	// Otherwise require a repo-wide unique value.
	return uniqueEndpointValue(vals, nil)
}

// uniqueEndpointValue collapses candidate const values to a single non-empty
// value, returning ok=false when the candidates disagree (ambiguous) or none
// carries a recorded value. When restrict is non-nil only ids present in it are
// considered (the same-file preference).
func uniqueEndpointValue(vals map[string]string, restrict map[string]bool) (string, bool) {
	found := ""
	for id, v := range vals {
		if restrict != nil && !restrict[id] {
			continue
		}
		if v == "" {
			continue
		}
		if found == "" {
			found = v
			continue
		}
		if v != found {
			return "", false // ambiguous — distinct candidate values
		}
	}
	if found == "" {
		return "", false
	}
	return found, true
}

// nodeInRepoScope reports whether a candidate const node belongs to the repo
// identified by repoPrefix. An empty repoPrefix disables scoping (single-repo
// callers and tests).
func nodeInRepoScope(n *graph.Node, repoPrefix string) bool {
	if repoPrefix == "" {
		return true
	}
	p := repoPrefix + "/"
	return strings.HasPrefix(n.ID, p) || strings.HasPrefix(n.FilePath, p)
}
