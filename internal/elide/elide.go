// Package elide compresses source code by replacing every function or
// method body with a one-line stub while preserving signatures,
// imports, top-level declarations, comments, and structure.
//
// Tree-sitter parses the source with the appropriate grammar for the
// language, then the walker collects the byte ranges of every
// function/method body and rebuilds the buffer with each range
// replaced by a per-language stub:
//
//   - Brace languages (Go, TS, JS, Java, C, C++, C#, Rust, PHP, Bash,
//     Kotlin, Scala): "{ /* N lines elided */ }"
//   - Indent languages (Python): "...  # N lines elided"
//   - Ruby: "# N lines elided"
//
// The package is intentionally fail-soft: an unsupported language, a
// missing grammar binding, or a tree-sitter parse failure all return
// the original source unchanged with a sentinel error. Callers (the
// compress_bodies path of get_symbol_source / get_editing_context /
// read_file) fall back to the raw source in that case.
package elide

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	bashlang "github.com/zzet/gortex/internal/parser/tsitter/bash"
	clang "github.com/zzet/gortex/internal/parser/tsitter/c"
	cpplang "github.com/zzet/gortex/internal/parser/tsitter/cpp"
	csharplang "github.com/zzet/gortex/internal/parser/tsitter/csharp"
	elixirlang "github.com/zzet/gortex/internal/parser/tsitter/elixir"
	golang "github.com/zzet/gortex/internal/parser/tsitter/golang"
	javalang "github.com/zzet/gortex/internal/parser/tsitter/java"
	jslang "github.com/zzet/gortex/internal/parser/tsitter/javascript"
	kotlinlang "github.com/zzet/gortex/internal/parser/tsitter/kotlin"
	phplang "github.com/zzet/gortex/internal/parser/tsitter/php"
	pylang "github.com/zzet/gortex/internal/parser/tsitter/python"
	rubylang "github.com/zzet/gortex/internal/parser/tsitter/ruby"
	rustlang "github.com/zzet/gortex/internal/parser/tsitter/rust"
	scalalang "github.com/zzet/gortex/internal/parser/tsitter/scala"
	tslang "github.com/zzet/gortex/internal/parser/tsitter/typescript"
	tsxlang "github.com/zzet/gortex/internal/parser/tsitter/tsx"
)

const parseTimeout = 5 * time.Second

var (
	// ErrUnsupportedLang is returned when the language is unknown or no
	// grammar binding is wired in. The original source is returned
	// alongside the error.
	ErrUnsupportedLang = errors.New("elide: unsupported language")
	// ErrParse is returned when tree-sitter could not parse the source.
	// The original source is returned alongside the error.
	ErrParse = errors.New("elide: parse failed")
)

type stubStyle int

const (
	stubBrace  stubStyle = iota // { /* N lines elided */ }
	stubPython                  // ...  # N lines elided
	stubRuby                    // # N lines elided
	stubElixir                  // # N lines elided  (between `do` and `end`)
)

// languageSpec describes how to find and elide function bodies in one
// tree-sitter grammar.
type languageSpec struct {
	grammarFn func() *sitter.Language
	// findBody locates the body node to elide inside a parent
	// declaration node. Returns nil when the declaration has no
	// elidable body (e.g. an abstract method, an arrow function with
	// expression body).
	findBody func(node *sitter.Node) *sitter.Node
	// parents lists the node kinds that are function/method
	// declarations whose body should be elided.
	parents map[string]struct{}
	style   stubStyle
}

func parents(kinds ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		m[k] = struct{}{}
	}
	return m
}

// fieldFinder builds a findBody that looks up a field by name and
// accepts only bodies whose kind is in the allowlist.
func fieldFinder(field string, bodyKinds ...string) func(*sitter.Node) *sitter.Node {
	allow := make(map[string]struct{}, len(bodyKinds))
	for _, k := range bodyKinds {
		allow[k] = struct{}{}
	}
	return func(node *sitter.Node) *sitter.Node {
		body := node.ChildByFieldName(field)
		if body == nil {
			return nil
		}
		if _, ok := allow[body.Type()]; !ok {
			return nil
		}
		return body
	}
}

// namedChildFinder builds a findBody that scans named children for
// the first one whose kind is in the allowlist. Used by Ruby's
// `method` (body is the `body_statement` named child, not a field).
func namedChildFinder(bodyKinds ...string) func(*sitter.Node) *sitter.Node {
	allow := make(map[string]struct{}, len(bodyKinds))
	for _, k := range bodyKinds {
		allow[k] = struct{}{}
	}
	return func(node *sitter.Node) *sitter.Node {
		n := int(node.NamedChildCount())
		for i := range n {
			c := node.NamedChild(i)
			if c == nil {
				continue
			}
			if _, ok := allow[c.Type()]; ok {
				return c
			}
		}
		return nil
	}
}

// kotlinBodyFinder locates the block body of a Kotlin function. The
// tree-sitter-kotlin grammar wraps the body in a `function_body` node
// (no `body` field name). A block-bodied function has function_body's
// source starting with `{`; the `fun foo() = expr` short form starts
// with `=` and is left untouched (already minimal).
func kotlinBodyFinder(node *sitter.Node) *sitter.Node {
	n := int(node.NamedChildCount())
	for i := range n {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() != "function_body" {
			continue
		}
		// Look at the first non-whitespace byte of the function_body
		// substring to detect block- vs expression-bodied. We don't
		// have the source bytes in scope here, so peek at the first
		// named child instead: a block body has a `statements` child
		// or is empty; an expression body has a single expression-kind
		// child (binary_expression, simple_identifier, call_expression, …).
		if c.NamedChildCount() == 0 {
			// Empty block (`fun foo() {}`); the function_body itself
			// is `{}` and elision will replace it with the stub.
			return c
		}
		first := c.NamedChild(0)
		if first != nil && first.Type() == "statements" {
			return c
		}
		// Expression-bodied — already minimal, skip.
		return nil
	}
	return nil
}

// elixirCallBody handles Elixir's `def name do ... end` shape. Tree-sitter-elixir
// represents a function as a `call` whose first child identifier is
// "def"/"defp"/"defmacro" and whose last argument is a `do_block`. We
// return the `do_block` for elision.
func elixirCallBody(node *sitter.Node) *sitter.Node {
	first := node.NamedChild(0)
	if first == nil {
		return nil
	}
	if first.Type() != "identifier" {
		return nil
	}
	// Caller would not call this if the node isn't a `call`, but we
	// still gate by macro name to avoid eliding arbitrary calls.
	// (The src bytes aren't here, so we compare via byte range —
	// kept simple: this function is only invoked when the parent
	// kind is "call" and the caller has already filtered by the
	// macro identifier via a separate hook, so the gate would
	// actually live elsewhere. See specs[elixir].findBody below.)
	for i := int(node.NamedChildCount()) - 1; i >= 0; i-- {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() == "do_block" {
			return c
		}
	}
	return nil
}

// elixirCallFilter is a per-language filter applied on top of the
// parents map: it returns true only when a `call` node looks like a
// `def`-family macro call. Used to avoid eliding arbitrary calls in
// elixir source.
func elixirCallFilter(node *sitter.Node, src []byte) bool {
	first := node.NamedChild(0)
	if first == nil || first.Type() != "identifier" {
		return false
	}
	name := first.Content(src)
	switch name {
	case "def", "defp", "defmacro", "defmacrop", "defguard", "defguardp":
		return true
	}
	return false
}

var (
	specsOnce sync.Once
	specs     map[string]*languageSpec
)

func initSpecs() {
	specs = map[string]*languageSpec{
		"go": {
			grammarFn: golang.GetLanguage,
			parents:   parents("function_declaration", "method_declaration"),
			findBody:  fieldFinder("body", "block"),
			style:     stubBrace,
		},
		"typescript": {
			grammarFn: tslang.GetLanguage,
			parents: parents(
				"function_declaration",
				"method_definition",
				"generator_function_declaration",
				"function_expression",
				"arrow_function",
			),
			// Only elide block-bodied arrow functions; expression-bodied
			// arrows are already minimal.
			findBody: fieldFinder("body", "statement_block"),
			style:    stubBrace,
		},
		"tsx": {
			grammarFn: tsxlang.GetLanguage,
			parents: parents(
				"function_declaration",
				"method_definition",
				"generator_function_declaration",
				"function_expression",
				"arrow_function",
			),
			findBody: fieldFinder("body", "statement_block"),
			style:    stubBrace,
		},
		"javascript": {
			grammarFn: jslang.GetLanguage,
			parents: parents(
				"function_declaration",
				"method_definition",
				"generator_function_declaration",
				"function_expression",
				"arrow_function",
			),
			findBody: fieldFinder("body", "statement_block"),
			style:    stubBrace,
		},
		"python": {
			grammarFn: pylang.GetLanguage,
			parents:   parents("function_definition"),
			findBody:  fieldFinder("body", "block"),
			style:     stubPython,
		},
		"rust": {
			grammarFn: rustlang.GetLanguage,
			parents:   parents("function_item"),
			findBody:  fieldFinder("body", "block"),
			style:     stubBrace,
		},
		"java": {
			grammarFn: javalang.GetLanguage,
			parents: parents(
				"method_declaration",
				"constructor_declaration",
			),
			findBody: fieldFinder("body", "block", "constructor_body"),
			style:    stubBrace,
		},
		"c": {
			grammarFn: clang.GetLanguage,
			parents:   parents("function_definition"),
			findBody:  fieldFinder("body", "compound_statement"),
			style:     stubBrace,
		},
		"cpp": {
			grammarFn: cpplang.GetLanguage,
			parents:   parents("function_definition", "template_function"),
			findBody:  fieldFinder("body", "compound_statement"),
			style:     stubBrace,
		},
		"csharp": {
			grammarFn: csharplang.GetLanguage,
			parents: parents(
				"method_declaration",
				"constructor_declaration",
				"destructor_declaration",
				"operator_declaration",
				"local_function_statement",
				"conversion_operator_declaration",
			),
			findBody: fieldFinder("body", "block"),
			style:    stubBrace,
		},
		"kotlin": {
			grammarFn: kotlinlang.GetLanguage,
			parents:   parents("function_declaration", "anonymous_function"),
			findBody:  kotlinBodyFinder,
			style:     stubBrace,
		},
		"scala": {
			grammarFn: scalalang.GetLanguage,
			parents: parents(
				"function_definition",
				"function_declaration",
			),
			// tree-sitter-scala wires the body as a plain `block` named
			// child (no `body` field). The `def x = expr` short form has
			// no block child and is left untouched.
			findBody: namedChildFinder("block", "indented_block"),
			style:    stubBrace,
		},
		"php": {
			grammarFn: phplang.GetLanguage,
			parents: parents(
				"function_definition",
				"method_declaration",
			),
			findBody: fieldFinder("body", "compound_statement"),
			style:    stubBrace,
		},
		"ruby": {
			grammarFn: rubylang.GetLanguage,
			parents: parents(
				"method",
				"singleton_method",
			),
			findBody: namedChildFinder("body_statement"),
			style:    stubRuby,
		},
		"bash": {
			grammarFn: bashlang.GetLanguage,
			parents:   parents("function_definition"),
			findBody:  fieldFinder("body", "compound_statement"),
			style:     stubBrace,
		},
		"elixir": {
			grammarFn: elixirlang.GetLanguage,
			parents:   parents("call"),
			findBody:  elixirCallBody,
			style:     stubElixir,
		},
	}
}

func getSpec(lang string) *languageSpec {
	specsOnce.Do(initSpecs)
	return specs[normalizeLang(lang)]
}

// Languages reports the canonical language codes elide knows how to
// compress. The returned slice is sorted and safe for the caller to
// retain; it is recomputed on every call so test-only manipulations
// don't bleed across goroutines.
func Languages() []string {
	specsOnce.Do(initSpecs)
	out := make([]string, 0, len(specs))
	for k := range specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normalizeLang(lang string) string {
	switch strings.ToLower(lang) {
	case "c++", "cpp", "cxx", "cc":
		return "cpp"
	case "c#", "csharp", "cs":
		return "csharp"
	case "js", "javascript", "jsx":
		return "javascript"
	case "ts", "typescript":
		return "typescript"
	case "tsx":
		return "tsx"
	case "py", "python":
		return "python"
	case "rb", "ruby":
		return "ruby"
	case "rs", "rust":
		return "rust"
	case "sh", "bash", "shell":
		return "bash"
	case "kt", "kotlin":
		return "kotlin"
	case "ex", "exs", "elixir":
		return "elixir"
	}
	return strings.ToLower(lang)
}

// IsSupported reports whether the language is wired in. The caller
// can use this to decide whether to pass compress_bodies=true upstream
// or skip the round-trip.
func IsSupported(lang string) bool {
	return getSpec(lang) != nil
}

// Compress returns a copy of src with every function or method body
// replaced by a per-language stub. The original src and a sentinel
// error are returned when the language is unsupported, when no
// grammar binding is available, or when tree-sitter parsing fails.
//
// Top-level constants, types, fields, imports, and comments are
// preserved verbatim because they have no body to elide; everything
// outside the collected body ranges is copied through byte-for-byte.
func Compress(src []byte, lang string) ([]byte, error) {
	if len(src) == 0 {
		return src, nil
	}
	spec := getSpec(lang)
	if spec == nil {
		return src, fmt.Errorf("%w: %q", ErrUnsupportedLang, lang)
	}
	grammar := spec.grammarFn()
	if grammar == nil {
		return src, fmt.Errorf("%w: %q (no grammar binding)", ErrUnsupportedLang, lang)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(grammar)
	ctx, cancel := context.WithTimeout(context.Background(), parseTimeout)
	defer cancel()
	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return src, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if tree == nil {
		return src, ErrParse
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil {
		return src, nil
	}
	ranges := collectRanges(root, spec, src)
	if len(ranges) == 0 {
		return src, nil
	}
	return applyRanges(src, ranges), nil
}

// CompressString is the string-flavoured wrapper around Compress.
func CompressString(src, lang string) (string, error) {
	out, err := Compress([]byte(src), lang)
	return string(out), err
}

type elideRange struct {
	startByte uint32
	endByte   uint32
	stub      string
}

func collectRanges(root *sitter.Node, spec *languageSpec, src []byte) []elideRange {
	var out []elideRange
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}
		kind := node.Type()
		if _, isParent := spec.parents[kind]; isParent {
			// Elixir gate: only `call` nodes whose head identifier is
			// a def-family macro qualify. Other languages don't need
			// a filter — their parent kinds are unambiguous.
			eligible := true
			if spec.style == stubElixir && kind == "call" {
				eligible = elixirCallFilter(node, src)
			}
			if eligible {
				if body := spec.findBody(node); body != nil {
					stub, lineCount := renderStub(spec.style, body, src)
					_ = lineCount
					out = append(out, elideRange{
						startByte: body.StartByte(),
						endByte:   body.EndByte(),
						stub:      stub,
					})
					return // do not recurse into elided body
				}
			}
		}
		n := int(node.NamedChildCount())
		for i := range n {
			walk(node.NamedChild(i))
		}
	}
	walk(root)
	return out
}

func renderStub(style stubStyle, body *sitter.Node, src []byte) (string, int) {
	startRow := int(body.StartPoint().Row)
	endRow := int(body.EndPoint().Row)
	switch style {
	case stubBrace:
		// {…} body: lines strictly between the opening and closing brace.
		// Single-line body { foo } collapses to 1.
		lines := max(endRow-startRow-1, 1)
		return fmt.Sprintf("{ /* %d lines elided */ }", lines), lines
	case stubPython:
		// The block node starts at the first non-whitespace byte of the
		// first body statement; the leading indent of that first line
		// is already in the source bytes BEFORE block.StartByte, so
		// we emit only the ellipsis stub.
		lines := max(endRow-startRow+1, 1)
		return fmt.Sprintf("...  # %d lines elided", lines), lines
	case stubRuby:
		// body_statement byte range is the inner statements; the
		// surrounding `def name`/`end` keywords stay verbatim. The
		// caller's original indent precedes body_statement.StartByte.
		lines := max(endRow-startRow+1, 1)
		return fmt.Sprintf("# %d lines elided", lines), lines
	case stubElixir:
		// do_block spans `do\n  ...\nend`. We collapse the inner
		// statements but keep the do/end keywords intact by emitting
		// a fresh `do\n  # N lines elided\nend` block. The indent of
		// the do-block opener is in the source bytes before
		// body.StartByte; we line our `do` up flush against it.
		lines := max(endRow-startRow-1, 1)
		indent := leadingIndent(src, body.StartByte())
		return fmt.Sprintf("do\n%s  # %d lines elided\n%send", indent, lines, indent), lines
	}
	return "", 0
}

// leadingIndent returns the run of spaces/tabs immediately preceding
// the byte offset (back to the most recent newline or start of file).
// Used by stubElixir so its emitted `end` lands on the column of the
// original `do` keyword.
func leadingIndent(src []byte, off uint32) string {
	if int(off) > len(src) {
		off = uint32(len(src))
	}
	i := int(off) - 1
	end := i
	for i >= 0 && (src[i] == ' ' || src[i] == '\t') {
		i--
	}
	if end < 0 || i+1 > end {
		return ""
	}
	return string(src[i+1 : end+1])
}

func applyRanges(src []byte, ranges []elideRange) []byte {
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].startByte < ranges[j].startByte
	})
	var b strings.Builder
	b.Grow(len(src))
	cursor := uint32(0)
	for _, r := range ranges {
		if r.startByte < cursor {
			continue
		}
		if int(r.startByte) > len(src) || int(r.endByte) > len(src) || r.endByte < r.startByte {
			continue
		}
		b.Write(src[cursor:r.startByte])
		b.WriteString(r.stub)
		cursor = r.endByte
	}
	if int(cursor) < len(src) {
		b.Write(src[cursor:])
	}
	return []byte(b.String())
}
