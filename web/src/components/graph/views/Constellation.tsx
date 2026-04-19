'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import {
  computeDegree, groupByRepo, stableSortByDegreeDesc, seededRng,
  layoutRepos, shortName,
} from './layout'

// Caps node count per repo so very large repos (100k+ nodes in the
// daedalus tree) don't blow up the SVG. The top-degree slice preserves
// the topologically interesting symbols.
const MAX_NODES_PER_REPO = 80
const MAX_EDGES = 800
const W = 1100
const H = 640

type Placed = {
  node: GortexNode
  x: number
  y: number
  size: number
  color: string
  repo: string
  label: boolean
  degree: number
}

export function GraphConstellation({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)

  const { placed, edges } = useMemo(() => {
    if (!graph) return { placed: [] as Placed[], edges: [] as [Placed, Placed, boolean][] }

    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visibleRepos = repos.filter((r) => !filterRepos.size || filterRepos.has(r.id))
    const layouts = layoutRepos(visibleRepos, buckets, W, H)

    const placed: Placed[] = []
    const byId = new Map<string, Placed>()
    for (const L of layouts) {
      const sorted = stableSortByDegreeDesc(L.nodes, degree).slice(0, MAX_NODES_PER_REPO)
      const rng = seededRng(hashString(L.id))
      sorted.forEach((n, i) => {
        const t = i / Math.max(1, sorted.length - 1)
        const r = L.radius * 0.95 * Math.sqrt(t + 0.02)
        const angle = i * 2.39996 + rng() * 0.2
        const x = L.cx + Math.cos(angle) * r
        const y = L.cy + Math.sin(angle) * r * 0.75
        const deg = degree.get(n.id) ?? 0
        const size = 1.8 + Math.min(6, Math.log2(deg + 1) * 1.1)
        const p: Placed = {
          node: n, x, y, size, color: L.color, repo: L.id,
          label: i < 4 || deg >= 10,
          degree: deg,
        }
        placed.push(p)
        byId.set(n.id, p)
      })
    }

    const sortedEdges = [...graph.edges].sort((a, b) => {
      const da = (degree.get(a.from) ?? 0) + (degree.get(a.to) ?? 0)
      const db = (degree.get(b.from) ?? 0) + (degree.get(b.to) ?? 0)
      return db - da
    })
    const edges: [Placed, Placed, boolean][] = []
    for (const e of sortedEdges) {
      const a = byId.get(e.from)
      const b = byId.get(e.to)
      if (!a || !b || a === b) continue
      edges.push([a, b, !!e.cross_repo])
      if (edges.length >= MAX_EDGES) break
    }

    return { placed, edges }
  }, [graph, repos, filterRepos])

  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="100%" style={{ display: 'block' }}>
      {edges.map(([a, b, cross], i) => (
        <line
          key={i}
          x1={a.x}
          y1={a.y}
          x2={b.x}
          y2={b.y}
          stroke={cross ? 'var(--accent)' : 'var(--line-2)'}
          strokeWidth={cross ? 0.6 : 0.3}
          opacity={cross ? 0.45 : 0.4}
        />
      ))}
      {placed.map((p, i) => (
        <g
          key={i}
          onClick={() =>
            setSym({
              id: p.node.id,
              kind: (p.node.kind as 'function') ?? 'function',
              name: p.node.name,
              repo: p.repo,
              file: p.node.file_path,
              sig: '',
              callers: 0,
              callees: 0,
              community: '',
              caveats: [],
            })
          }
          style={{ cursor: 'pointer' }}
        >
          <circle cx={p.x} cy={p.y} r={p.size} fill={p.color} opacity={0.85 + Math.min(0.15, p.degree * 0.01)} />
          {p.label && (
            <text
              x={p.x + p.size + 3}
              y={p.y + 3}
              fontFamily="JetBrains Mono"
              fontSize="10"
              fill="var(--fg-1)"
              opacity="0.85"
            >
              {shortName(p.node, 22)}
            </text>
          )}
        </g>
      ))}
      {placed.length === 0 && (
        <text x={W / 2} y={H / 2} textAnchor="middle" fill="var(--fg-3)" fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
    </svg>
  )
}

function hashString(s: string): number {
  let h = 2166136261 >>> 0
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619) >>> 0
  }
  return h
}
