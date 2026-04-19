'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc } from './layout'

const MAX_PER_REPO = 22

type ONode = {
  x: number; y: number; theta: number; ring: number
  color: string; repo: string; hot: boolean; size: number
  id: string; node: GortexNode
}

export function ThreeDOrbital({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)
  const cx = 540
  const cy = 320
  const rings = [70, 125, 185, 248, 315]
  const tilt = 0.45

  const { nodes, arcs, radials, visibleRepos } = useMemo(() => {
    if (!graph) return { nodes: [] as ONode[], arcs: [], radials: [], visibleRepos: [] as Repo[] }
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visibleRepos = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)

    const nodes: ONode[] = []
    const nodeIndex = new Map<string, ONode>()
    const globalMaxDeg = Math.max(1, ...visibleRepos.flatMap(
      (r) => (buckets.get(r.id) ?? []).map((n) => degree.get(n.id) ?? 0),
    ))
    visibleRepos.forEach((rep, idx) => {
      const sorted = stableSortByDegreeDesc(buckets.get(rep.id) ?? [], degree).slice(0, MAX_PER_REPO)
      const sectorSpan = (Math.PI * 2) / Math.max(1, visibleRepos.length)
      const sectorStart = idx * sectorSpan
      const step = sectorSpan / Math.max(1, sorted.length + 1)
      sorted.forEach((n, i) => {
        const deg = degree.get(n.id) ?? 0
        // Higher-degree nodes ride the inner rings (closer to core).
        const ringIdx = Math.min(
          rings.length - 1,
          Math.max(0, rings.length - 1 - Math.round((deg / globalMaxDeg) * (rings.length - 1))),
        )
        const theta = sectorStart + step * (i + 1)
        const x = cx + Math.cos(theta) * rings[ringIdx]
        const y = cy + Math.sin(theta) * rings[ringIdx] * tilt
        const node: ONode = {
          x, y, theta, ring: ringIdx, color: rep.color, repo: rep.id,
          hot: deg >= Math.max(8, globalMaxDeg * 0.7),
          size: 1.8 + Math.min(3.5, Math.log2(deg + 1) * 0.7),
          id: n.id, node: n,
        }
        nodes.push(node)
        nodeIndex.set(n.id, node)
      })
    })

    const arcs: { a: ONode; b: ONode; ring: number; hot: boolean }[] = []
    const radials: { a: ONode; b: ONode; hot: boolean }[] = []
    for (const e of graph.edges) {
      const a = nodeIndex.get(e.from)
      const b = nodeIndex.get(e.to)
      if (!a || !b || a === b) continue
      if (a.ring === b.ring) {
        arcs.push({ a, b, ring: a.ring, hot: !!e.cross_repo })
      } else {
        radials.push({ a, b, hot: !!e.cross_repo })
      }
      if (arcs.length + radials.length >= 80) break
    }
    return { nodes, arcs, radials, visibleRepos }
  }, [graph, repos, filterRepos])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {rings.map((rr, i) => (
        <ellipse key={i} cx={cx} cy={cy} rx={rr} ry={rr * tilt}
          fill="none" stroke="var(--line-1)" strokeWidth="0.6"
          strokeDasharray={i === 0 ? '0' : '2 4'} />
      ))}
      {visibleRepos.map((rep, idx) => {
        const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2
        const rr = rings[rings.length - 1]
        const x = cx + Math.cos(a) * rr
        const y = cy + Math.sin(a) * rr * tilt
        return <line key={rep.id} x1={cx} y1={cy} x2={x} y2={y}
          stroke={rep.color} strokeOpacity="0.18" strokeWidth="0.6" />
      })}
      {radials.map((e, i) => (
        <line key={`r${i}`} x1={e.a.x} y1={e.a.y} x2={e.b.x} y2={e.b.y}
          stroke={e.hot ? 'var(--pink)' : 'var(--accent)'}
          strokeOpacity={e.hot ? 0.85 : 0.3}
          strokeWidth={e.hot ? 1.3 : 0.6} />
      ))}
      {arcs.map((e, i) => {
        const rr = rings[e.ring]
        const mid = { x: (e.a.x + e.b.x) / 2, y: (e.a.y + e.b.y) / 2 }
        const dx = mid.x - cx
        const dy = (mid.y - cy) / tilt
        const len = Math.hypot(dx, dy) || 1
        const out = (rr * 1.05) / len
        const ctrl = { x: cx + dx * out, y: cy + dy * out * tilt }
        return (
          <path key={`a${i}`} d={`M${e.a.x},${e.a.y} Q${ctrl.x},${ctrl.y} ${e.b.x},${e.b.y}`}
            fill="none"
            stroke={e.hot ? 'var(--pink)' : e.a.color}
            strokeOpacity={e.hot ? 0.8 : 0.4}
            strokeWidth={e.hot ? 1.2 : 0.6} />
        )
      })}
      {visibleRepos.map((rep, idx) => {
        const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2 + Math.PI / Math.max(1, visibleRepos.length)
        const rr = rings[rings.length - 1] + 20
        const x = cx + Math.cos(a) * rr
        const y = cy + Math.sin(a) * rr * tilt
        return (
          <g key={rep.id}>
            <circle cx={x} cy={y} r={3} fill={rep.color} />
            <text x={x + 8} y={y + 3} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
      {nodes.map((n, i) => (
        <circle key={i} cx={n.x} cy={n.y} r={n.hot ? n.size + 1 : n.size}
          fill={n.hot ? 'var(--pink)' : n.color} opacity="0.95"
          style={{ cursor: 'pointer' }}
          onClick={() => setSym({
            id: n.node.id,
            kind: (n.node.kind as 'function') ?? 'function',
            name: n.node.name,
            repo: n.repo,
            file: n.node.file_path,
            sig: '', callers: 0, callees: 0, community: '', caveats: [],
          })}
        />
      ))}
      <circle cx={cx} cy={cy} r={18} fill="var(--bg-1)" stroke="var(--accent)" strokeWidth="1.5" />
      <circle cx={cx} cy={cy} r={8} fill="var(--accent)" />
      {nodes.length === 0 && (
        <text x={cx} y={cy + 160} textAnchor="middle" fill="var(--fg-3)"
          fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
    </svg>
  )
}
