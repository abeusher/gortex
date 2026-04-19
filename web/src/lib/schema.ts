// UI-shaped types matching the new /v1/* endpoints (server/dashboard.go).
// Kept narrow on purpose — pages should consume these and never the raw
// MCP tool payloads, so a server-side reshape doesn't ripple through
// every component.

export type Repo = {
  id: string
  owner: string
  lang: string
  nodes: number
  edges: number
  funcs: number
  methods: number
  types: number
  interfaces: number
  vars: number
  files: number
  color: string
}

export type Process = {
  id: string
  name: string
  entry: string
  steps: number
  files: number
  repos: number
  score: number
  risk: 'ok' | 'warn' | 'risk'
  crosses: string[]
}

export type ContractKind = 'REST' | 'EVENT' | 'URL' | 'ENV' | 'DEP'
export type ContractType =
  | 'http'
  | 'grpc'
  | 'graphql'
  | 'topic'
  | 'ws'
  | 'env'
  | 'openapi'
  | 'dependency'
export type ContractScope = 'own' | 'external'

export type ContractLocation = {
  role: 'provider' | 'consumer' | string
  repo_prefix: string
  symbol_id: string
  file_path: string
  line: number
  meta?: Record<string, unknown>
}

export type Contract = {
  id: string
  name: string
  kind: ContractKind | string
  type: ContractType | string
  scope: ContractScope
  producer: string
  consumers: string[]
  version: string
  breaking: boolean
  callers: number
  last: string
  locations: ContractLocation[]
}

export type Community = {
  id: string
  name: string
  repo: string
  symbols: number
  files: number
  cohesion: number
}

export type Guard = {
  id: string
  name: string
  kind: string
  status: 'ok' | 'warn' | 'violated' | string
  hits: number
  scope: string
}

export type Caveat = {
  id: string
  severity: 'risk' | 'hot' | 'cycle' | 'unowned' | 'deprecated' | 'boundary'
  symbol: string
  title: string
  desc: string
  owner: string
  age: string
}

export type Activity = {
  file_path: string
  kind: 'created' | 'modified' | 'deleted' | 'renamed' | string
  nodes_added: number
  nodes_removed: number
  edges_added: number
  edges_removed: number
  timestamp: string
  duration_ms: number
}

export type KindCount = { name: string; count: number }
export type LanguageCount = { name: string; count: number }

export type DashboardSnapshot = {
  stats: {
    total_nodes: number
    total_edges: number
    repos: number
    caveats: number
    version: string
  }
  kinds: KindCount[]
  languages: LanguageCount[]
  repos: Repo[]
  activity: Activity[]
  caveats: Caveat[]
  processes: Process[]
}
