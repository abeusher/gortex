package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// NestMicroserviceExtractor detects NestJS microservice message handlers —
// @MessagePattern (request/response) and @EventPattern (fire-and-forget) — and
// records each as a topic contract on the pattern it serves.
type NestMicroserviceExtractor struct{}

func (e *NestMicroserviceExtractor) SupportedLanguages() []string {
	return []string{"typescript", "javascript"}
}

var (
	// nestMsgPatternRe matches a @MessagePattern( / @EventPattern( decorator.
	nestMsgPatternRe = regexp.MustCompile(`@(MessagePattern|EventPattern)\s*\(`)
	// nestPatternStringRe pulls the first quoted token out of the decorator
	// args, covering both `'cmd'` and `{ cmd: 'cmd' }` pattern forms.
	nestPatternStringRe = regexp.MustCompile(`["']([^"']+)["']`)
	// nestMethodDefRe matches the handler method definition a decorator wraps.
	nestMethodDefRe = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|readonly\s+|async\s+|static\s+)*([A-Za-z_]\w*)\s*\(`)
)

// Extract scans for @MessagePattern / @EventPattern handlers.
func (e *NestMicroserviceExtractor) Extract(filePath string, src []byte, nodes []*graph.Node, edges []*graph.Edge) []Contract {
	text := string(src)
	if !strings.Contains(text, "@MessagePattern(") && !strings.Contains(text, "@EventPattern(") {
		return nil
	}
	lines := strings.Split(text, "\n")
	fileNodes := filterFileNodes(filePath, nodes)

	var out []Contract
	for i, line := range lines {
		m := nestMsgPatternRe.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		kind := line[m[2]:m[3]] // MessagePattern | EventPattern
		pm := nestPatternStringRe.FindStringSubmatch(line[m[1]:])
		if pm == nil {
			continue
		}
		pattern := pm[1]
		handlerName, _ := nestHandlerAfter(lines, i)
		symbolID := ""
		if handlerName != "" {
			symbolID = findFunctionByName(fileNodes, handlerName)
		}
		out = append(out, Contract{
			ID:       fmt.Sprintf("topic::%s", pattern),
			Type:     ContractTopic,
			Role:     RoleProvider,
			SymbolID: symbolID,
			FilePath: filePath,
			Line:     i + 1,
			Meta: map[string]any{
				"pattern":      pattern,
				"message_kind": kind,
				"framework":    "nestjs",
			},
			Confidence: 0.9,
		})
	}
	return out
}

// nestHandlerAfter returns the name and 1-based line of the method a NestJS
// decorator stack wraps: the first method definition below fromIdx, skipping
// blank lines and further decorators. Shared across the GraphQL, websocket and
// microservice NestJS passes.
func nestHandlerAfter(lines []string, fromIdx int) (name string, lineNum int) {
	for j := fromIdx + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "@") || strings.HasPrefix(t, "//") {
			continue
		}
		if m := nestMethodDefRe.FindStringSubmatch(lines[j]); m != nil {
			return m[1], j + 1
		}
		return "", 0
	}
	return "", 0
}
