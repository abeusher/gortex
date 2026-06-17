package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"go.uber.org/zap"
)

// TestReadSinkRedactionGolden is the cross-tool regression guard for config-leaf
// secret redaction. Cases 1-4 drive the real read_file handler end-to-end (so a
// regression that drops read_file's redaction call fails here); case 5 pins the
// shared chokepoint contract that get_symbol_source and smart_context also use.
func TestReadSinkRedactionGolden(t *testing.T) {
	g := graph.New()
	srv := NewServer(query.NewEngine(g), g, nil, nil, zap.NewNop(), nil)

	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := func(args map[string]any) (string, map[string]any) {
		m := unmarshalResult(t, callHandler(t, srv.handleReadFile, args))
		c, _ := m["content"].(string)
		return c, m
	}

	const secret = "AKIAIOSFODNN7EXAMPLE" // value-shape AWS access-key id

	// 1. config-leaf secret withheld by default; benign keys/values survive.
	secretYAML := write("secrets.yaml", "service:\n  name: billing\n  api_key: "+secret+"\n  timeout: 30\n")
	c, m := read(map[string]any{"path": secretYAML})
	if strings.Contains(c, secret) {
		t.Errorf("read_file leaked a config-leaf secret by default: %q", c)
	}
	if !strings.Contains(c, "name: billing") || !strings.Contains(c, "timeout: 30") {
		t.Errorf("read_file dropped benign config alongside the secret: %q", c)
	}
	if m["secrets_redacted"] != true {
		t.Errorf("expected secrets_redacted=true, got %v", m["secrets_redacted"])
	}

	// 2. allow_secrets serves the secret verbatim.
	if c2, _ := read(map[string]any{"path": secretYAML, "allow_secrets": true}); !strings.Contains(c2, secret) {
		t.Errorf("allow_secrets:true should serve verbatim, got %q", c2)
	}

	// 3. a benign-only config file is returned byte-identical and unflagged.
	const benign = "port: 8080\nname: web\n"
	benignYAML := write("config.yaml", benign)
	if c3, m3 := read(map[string]any{"path": benignYAML}); c3 != benign || m3["secrets_redacted"] == true {
		t.Errorf("benign config should be byte-identical and unflagged: content=%q flag=%v", c3, m3["secrets_redacted"])
	}

	// 4. a code file with a secret literal is NOT redacted (config-leaf only).
	goFile := write("main.go", "package main\n\nconst k = \""+secret+"\"\n")
	if c4, _ := read(map[string]any{"path": goFile}); !strings.Contains(c4, secret) {
		t.Errorf("code files must not be redacted (config-leaf only): %q", c4)
	}

	// 5. shared chokepoint contract — the policy every read sink applies.
	cases := []struct {
		name, lang, path, body string
		allow, wantRedacted    bool
	}{
		{"yaml secret", "yaml", "a.yaml", "k: " + secret + "\n", false, true},
		{"env secret", "dotenv", ".env", "K=" + secret + "\n", false, true},
		{"properties secret", "properties", "a.properties", "k=" + secret + "\n", false, true},
		{"toml secret", "toml", "a.toml", "k = \"" + secret + "\"\n", false, true},
		{"yaml allow_secrets", "yaml", "a.yaml", "k: " + secret + "\n", true, false},
		{"go literal", "go", "a.go", "k := \"" + secret + "\"\n", false, false},
		{"yaml benign", "yaml", "a.yaml", "port: 8080\n", false, false},
	}
	for _, tc := range cases {
		if _, did := srv.maybeRedactConfigLeaf(tc.lang, tc.path, tc.allow, tc.body); did != tc.wantRedacted {
			t.Errorf("chokepoint %q: redacted=%v want %v", tc.name, did, tc.wantRedacted)
		}
	}
}
