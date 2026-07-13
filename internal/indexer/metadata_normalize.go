package indexer

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// normalizeExtractionMetadata adds retrieval-only metadata at the shared
// extraction boundary. Parser-owned signature, documentation, and QualName
// values remain untouched: resolvers may rely on their exact representation.
func normalizeExtractionMetadata(result *parser.ExtractionResult, src []byte) {
	if result == nil {
		return
	}
	var sourceLines []string
	if len(src) > 0 {
		sourceLines = strings.Split(string(src), "\n")
	}

	nodesByID := make(map[string]*graph.Node, len(result.Nodes))
	for _, n := range result.Nodes {
		if n != nil {
			nodesByID[n.ID] = n
		}
	}
	owners := make(map[string]string)
	for _, edge := range result.Edges {
		if edge == nil || edge.Kind != graph.EdgeMemberOf || owners[edge.From] != "" {
			continue
		}
		if owner := nodesByID[edge.To]; owner != nil {
			owners[edge.From] = firstNonEmpty(owner.QualName, owner.Name)
		} else {
			owners[edge.From] = ownerNameFromID(edge.To)
		}
	}

	for _, n := range result.Nodes {
		if n == nil || n.Name == "" {
			continue
		}
		if !shouldNormalizeDefinitionMetadata(n.Kind) {
			// Params, locals, imports, builtins, and synthetic graph entities
			// often share their owner's StartLine. Deriving from source would
			// copy the enclosing declaration and doc into every child node.
			graph.SetRetrievalMetadata(n, graph.RetrievalMetadata{})
			continue
		}
		sig := normalizedMetaString(n.Meta, "signature")
		if sig == "" || syntheticSignature(sig, n.Name) {
			if derived := declarationSignature(sourceLines, n); derived != "" {
				sig = derived
			}
		}

		doc := normalizedDoc(metaString(n.Meta, "doc"))
		if doc == "" {
			doc = docAbove(sourceLines, n.StartLine)
		}

		qual := strings.TrimSpace(n.QualName)
		if qual == "" {
			owner := normalizedMetaString(n.Meta, "receiver")
			if owner == "" {
				owner = owners[n.ID]
			}
			if owner != "" {
				qual = joinQualified(owner, n.Name)
			}
		}
		graph.SetRetrievalMetadata(n, graph.RetrievalMetadata{
			Signature: sig,
			QualName:  qual,
			Doc:       doc,
		})
	}
}

func shouldNormalizeDefinitionMetadata(kind graph.NodeKind) bool {
	switch kind {
	case graph.KindFunction,
		graph.KindMethod,
		graph.KindType,
		graph.KindInterface,
		graph.KindVariable,
		graph.KindField,
		graph.KindClosure,
		graph.KindConstant,
		graph.KindEnumMember,
		graph.KindMacro:
		return true
	default:
		return false
	}
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, _ := meta[key].(string)
	return v
}

func normalizedMetaString(meta map[string]any, key string) string {
	return strings.Join(strings.Fields(metaString(meta, key)), " ")
}

func syntheticSignature(sig, name string) bool {
	compact := strings.ReplaceAll(sig, " ", "")
	return strings.Contains(compact, name+"(...)") ||
		compact == "function"+name+"()" ||
		compact == "fn"+name+"(...)"
}

// declarationSignature extracts only a declaration header from the node's
// source range. It is deliberately retrieval-only and bounded, so an
// imperfect language heuristic can never change symbol identity or resolution.
func declarationSignature(lines []string, n *graph.Node) string {
	if len(lines) == 0 || n == nil || n.StartLine < 1 {
		return ""
	}
	start := n.StartLine - 1
	if start >= len(lines) {
		return ""
	}
	end := start + 12
	if n.EndLine > n.StartLine && n.EndLine < end {
		end = n.EndLine
	}
	if end > len(lines) {
		end = len(lines)
	}
	candidate := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if candidate == "" {
		return ""
	}
	if len(candidate) > 2048 {
		candidate = candidate[:2048]
	}

	parenDepth, bracketDepth := 0, 0
	quote := rune(0)
	escaped := false
	cut := len(candidate)
	for i, r := range candidate {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{', ';':
			if parenDepth == 0 && bracketDepth == 0 {
				cut = i
			}
		}
		if cut != len(candidate) {
			break
		}
	}
	// Languages whose declarations have no brace or semicolon (for example
	// Python and Ruby) still get a bounded header rather than body text.
	if cut == len(candidate) {
		if newline := strings.IndexByte(candidate, '\n'); newline >= 0 {
			cut = newline
		}
	}
	candidate = strings.Join(strings.Fields(candidate[:cut]), " ")
	if candidate == "" || !strings.Contains(candidate, n.Name) {
		return ""
	}
	if len(candidate) > 512 {
		candidate = candidate[:512]
	}
	return strings.TrimSpace(candidate)
}

func normalizedDoc(doc string) string {
	if doc == "" {
		return ""
	}
	lines := strings.Split(doc, "\n")
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		line = strings.TrimSpace(strings.TrimPrefix(line, "/**"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "/*"))
		line = strings.TrimSpace(strings.TrimSuffix(line, "*/"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "///"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "//!"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		lines[i] = line
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func docAbove(lines []string, startLine int) string {
	if len(lines) == 0 || startLine <= 1 {
		return ""
	}
	i := startLine - 2
	for skipped := 0; i >= 0 && skipped < 3; skipped++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "@") || strings.HasPrefix(line, "#[") {
			i--
			continue
		}
		break
	}
	if i < 0 {
		return ""
	}

	if strings.HasSuffix(strings.TrimSpace(lines[i]), "*/") {
		end := i
		for i >= 0 {
			if strings.Contains(lines[i], "/*") {
				return normalizedDoc(strings.Join(lines[i:end+1], "\n"))
			}
			i--
		}
		return ""
	}

	end := i
	for i >= 0 {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "# ") {
			break
		}
		i--
	}
	if end == i {
		return ""
	}
	return normalizedDoc(strings.Join(lines[i+1:end+1], "\n"))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func ownerNameFromID(id string) string {
	if id == "" || strings.HasPrefix(id, "unresolved::") {
		return ""
	}
	if idx := strings.LastIndex(id, "::"); idx >= 0 {
		id = id[idx+2:]
	}
	if idx := strings.LastIndexAny(id, ".#"); idx >= 0 {
		id = id[idx+1:]
	}
	return strings.TrimSpace(id)
}

func joinQualified(owner, name string) string {
	owner = strings.TrimRight(strings.TrimSpace(owner), ".:")
	name = strings.TrimSpace(name)
	if owner == "" || name == "" {
		return ""
	}
	if owner == name || strings.HasSuffix(owner, "."+name) || strings.HasSuffix(owner, "::"+name) {
		return owner
	}
	return owner + "." + name
}
