# Gortex

[![CI](https://github.com/zzet/gortex/actions/workflows/ci.yml/badge.svg)](https://github.com/zzet/gortex/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/zzet/gortex)](https://goreportcard.com/report/github.com/zzet/gortex)

Code intelligence engine that indexes repositories into an in-memory knowledge graph and exposes it via CLI, MCP Server, and web UI.

Built for AI coding agents (Claude Code, Cursor, Codex) — one `get_editing_context` call replaces 5-10 file reads, cutting token usage by ~94%.

## Features

- **Knowledge graph** — every file, symbol, import, call chain, and type relationship in one queryable structure
- **7 languages** — Go, TypeScript, Python, Rust, Java, Ruby, Elixir (via tree-sitter)
- **22 MCP tools** — symbol lookup, call chains, blast radius, community detection, process discovery
- **Watch mode** — surgical graph updates on file change, live sync with agents
- **Web UI** — Sigma.js force-directed visualization with node size proportional to importance
- **IMPLEMENTS inference** — structural interface satisfaction detection for Go, TypeScript, Java, Rust
- **Zero dependencies** — everything runs in-process, in memory, no external services

## Quick Start

```bash
# Build (requires CGO for tree-sitter C bindings)
go build -o gortex ./cmd/gortex/

# Index a repo and print stats
gortex status --index /path/to/repo

# Start MCP server with watch mode
gortex serve --index /path/to/repo --watch

# Set up Gortex for a project (creates .mcp.json, .claude/commands/, CLAUDE.md)
gortex init /path/to/repo
```

## Usage with Claude Code

After running `gortex init` in your project, Claude Code automatically starts Gortex via `.mcp.json`. The agent gets:

- `/gortex-guide` — tools reference and graph schema
- `/gortex-explore` — architecture exploration workflow
- `/gortex-debug` — debugging workflow
- `/gortex-impact` — blast radius analysis
- `/gortex-refactor` — safe refactoring workflow

## CLI Commands

```
gortex init [path]           Set up Gortex for a project
gortex serve [flags]         Start the MCP server
gortex index [path]          Index a repository and print stats
gortex status [flags]        Show index status
gortex query <subcommand>    Query the knowledge graph
gortex clean                 Remove Gortex files from a project
gortex claude-md [flags]     Generate CLAUDE.md block
gortex version               Print version
```

### Query Subcommands

```
gortex query symbol <name>              Find symbols matching name
gortex query deps <id>                  Show dependencies
gortex query dependents <id>            Show blast radius
gortex query callers <func-id>          Show who calls a function
gortex query calls <func-id>            Show what a function calls
gortex query implementations <iface>    Show interface implementations
gortex query usages <id>                Show all usages
gortex query stats                      Show graph statistics
```

All query commands support `--format text|json|dot` (DOT output for Graphviz visualization).

## MCP Tools

### Core Navigation
| Tool | Description |
|------|-------------|
| `graph_stats` | Node/edge counts by kind and language |
| `search_symbols` | Find symbols by name (replaces Grep) |
| `get_symbol` | Symbol location and signature (replaces Read) |
| `get_file_summary` | All symbols and imports in a file |
| `get_editing_context` | **Primary pre-edit tool** — symbols, signatures, callers, callees |

### Graph Traversal
| Tool | Description |
|------|-------------|
| `get_dependencies` | What a symbol depends on |
| `get_dependents` | What depends on a symbol (blast radius) |
| `get_call_chain` | Forward call graph |
| `get_callers` | Reverse call graph |
| `find_usages` | Every reference to a symbol |
| `find_implementations` | Types implementing an interface |
| `get_cluster` | Bidirectional neighborhood |

### Coding Workflow
| Tool | Description |
|------|-------------|
| `get_symbol_signature` | Just the signature, no body |
| `find_import_path` | Correct import path for a symbol |
| `explain_change_impact` | Risk-tiered blast radius with affected processes |
| `get_recent_changes` | Files/symbols changed since timestamp |

### Analysis
| Tool | Description |
|------|-------------|
| `get_communities` | Functional clusters (Louvain community detection) |
| `get_community` | Members and cohesion for one community |
| `get_processes` | Discovered execution flows |
| `get_process` | Step-by-step trace of an execution flow |
| `detect_changes` | Git diff mapped to affected symbols |

## Web UI

When running `gortex serve`, a web visualization is available at `http://localhost:8765`:

- Sigma.js force-directed graph with ForceAtlas2 layout
- Node size proportional to degree (connection count = importance)
- Color-coded by kind (function, type, interface, method, variable, file)
- Real-time updates via SSE when watch mode is active
- Filter by node kind, hide test files, search by name
- Click nodes to highlight neighborhood

## Architecture

```
gortex binary
  CLI (cobra)  ──> Indexer ──> In-Memory Graph
  MCP Server ─────────────────> Query Engine
  Web Server ─────────────────> (Nodes + Edges + Indexes)
                   Watcher <── filesystem events (fsnotify)
```

**Data flow:**
1. Indexer walks the directory, dispatches files to language-specific extractors (tree-sitter)
2. Extractors produce nodes (files, functions, types, etc.) and edges (calls, imports, defines, etc.)
3. Resolver links cross-file references and infers interface implementations
4. Query Engine answers traversal queries over the live graph
5. Watcher detects changes and surgically patches the graph (debounced per-file)

## Graph Schema

**Node kinds:** `file`, `function`, `method`, `type`, `interface`, `variable`, `import`, `package`

**Edge kinds:** `calls`, `imports`, `defines`, `implements`, `extends`, `references`, `member_of`, `instantiates`

## Language Support (23 languages)

### Code Languages
| Language | Functions | Methods + MemberOf | Types | Interfaces | Imports | Calls | Variables |
|----------|-----------|-------------------|-------|------------|---------|-------|-----------|
| Go | Full | Full (receiver) | Full | Full + Meta["methods"] | Full | Full | Full |
| TypeScript | Full | Full | Full | Full + Meta["methods"] | Full | Full | Full |
| JavaScript | Full | Full | Full | - | Full | Full | Full |
| Python | Full | Full | Full | - | Full | Full | Partial |
| Rust | Full | Full (impl blocks) | Full | Full + Meta["methods"] | Full | Full | Full |
| Java | Full | Full | Full | Full + Meta["methods"] | Full | Full | Fields |
| Kotlin | Full | Full | Full | Full | Full | Full | Properties |
| Scala | Full | Full | Full | Full + Meta["methods"] | Full | Full | - |
| Swift | Full | Full | Full | Full + Meta["methods"] | Full | Full | - |
| PHP | Full | Full | Full | Full | Full | Full | - |
| Ruby | Full | Full | Full | - | Full | Full | Constants |
| Elixir | Full | Full (defmodule) | Modules | - | Full | Full | Attributes |
| C | Full | - | Structs/Enums | - | Full | Full | Globals |
| C++ | Full | Full | Classes/Structs | - | Full | Full | - |
| Bash | Full | - | - | - | source/. | Full | Exports |

### Data & Config Languages
| Language | What it extracts |
|----------|-----------------|
| SQL | Tables (with columns), views, functions, indexes, triggers |
| Protobuf | Messages (with fields), services + RPCs, enums, imports |
| HTML | Script/link references, element IDs |
| CSS | Class selectors, ID selectors, custom properties, @import |
| YAML | Top-level keys |
| TOML | Tables, key-value pairs |
| HCL | Resource/data/module/variable/output blocks |
| Dockerfile | FROM (base images), ENV/ARG variables |

## Building

```bash
# Build
go build -o gortex ./cmd/gortex/

# Test
go test -race ./...

# Build with version
go build -ldflags "-X main.version=v0.3.0" -o gortex ./cmd/gortex/
```

Requires Go 1.21+ and CGO enabled (for tree-sitter C bindings).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on adding features, language extractors, and submitting PRs.
