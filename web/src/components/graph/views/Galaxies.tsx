'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc, seededRng } from './layout'

const MAX_PER_REPO = 40

type GNode = {
  x: number; y: number; z: number; color: string; repo: string
  hot: boolean; id: string; node: GortexNode
}
type GProj = GNode & { s: number }

export function ThreeDGalaxies({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)
  const cx = 540
  const cy = 320

  const { projected, edges, visibleRepos } = useMemo(() => {
    if (!graph) return { projected: [] as GProj[], edges: [] as { a: GProj; b: GProj; same: boolean; hot: boolean }[], visibleRepos: [] as Repo[] }
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visibleRepos = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)

    const nodes: GNode[] = []
    const nodeIndex = new Map<string, GNode>()
    visibleRepos.forEach((rep, idx) => {
      const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2
      const gx = cx + Math.cos(a) * 240
      const gy = cy + Math.sin(a) * 150
      const rng = seededRng(hash(rep.id) + 59)
      const gz = (rng() - 0.5) * 200
      const sorted = stableSortByDegreeDesc(buckets.get(rep.id) ?? [], degree).slice(0, MAX_PER_REPO)
      const maxDeg = Math.max(1, ...sorted.map((n) => degree.get(n.id) ?? 0))
      sorted.forEach((n) => {
        const rr = Math.sqrt(rng()) * 110
        const t = rng() * Math.PI * 2
        const dz = (rng() - 0.5) * 120
        const deg = degree.get(n.id) ?? 0
        const node: GNode = {
          x: gx + Math.cos(t) * rr,
          y: gy + Math.sin(t) * rr * 0.8,
          z: gz + dz,
          color: rep.color, repo: rep.id,
          hot: deg >= Math.max(8, maxDeg * 0.7),
          id: n.id, node: n,
        }
        nodes.push(node)
        nodeIndex.set(n.id, node)
      })
    })
    const camZ = 600
    const proj = (n: GNode): GProj => {
      const s = camZ / (camZ - n.z)
      return { ...n, x: cx + (n.x - cx) * s, y: cy + (n.y - cy) * s, s }
    }
    const projectedNodes = nodes.map(proj)
    const projIndex = new Map<string, GProj>()
    projectedNodes.forEach((p) => projIndex.set(p.id, p))
    const projected = [...projectedNodes].sort((a, b) => a.z - b.z)

    const edges: { a: GProj; b: GProj; same: boolean; hot: boolean }[] = []
    for (const e of graph.edges) {
      const a = projIndex.get(e.from)
      const b = projIndex.get(e.to)
      if (!a || !b || a === b) continue
      const same = a.repo === b.repo
      edges.push({ a, b, same, hot: !!e.cross_repo && e.kind === 'calls' })
      if (edges.length >= 220) break
    }
    return { projected, edges, visibleRepos }
  }, [graph, repos, filterRepos])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {visibleRepos.map((rep, idx) => {
        const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2
        const gx = cx + Math.cos(a) * 240
        const gy = cy + Math.sin(a) * 150
        return <circle key={rep.id} cx={gx} cy={gy} r={130} fill={rep.color} fillOpacity="0.05" />
      })}
      {edges.map((e, i) => {
        const depth = (e.a.z + e.b.z) / 2
        const dim = Math.max(0.12, Math.min(1, (depth + 200) / 500))
        return (
          <line key={i} x1={e.a.x} y1={e.a.y} x2={e.b.x} y2={e.b.y}
            stroke={e.hot ? 'var(--pink)' : e.same ? e.a.color : 'var(--accent)'}
            strokeOpacity={(e.hot ? 0.85 : e.same ? 0.28 : 0.5) * dim}
            strokeWidth={e.hot ? 1.4 : e.same ? 0.4 : 0.7} />
        )
      })}
      {projected.map((n, i) => {
        const dim = Math.max(0.35, Math.min(1, (n.z + 200) / 500))
        const size = Math.max(1.2, 2.4 * n.s)
        return (
          <circle key={i} cx={n.x} cy={n.y} r={n.hot ? size + 1 : size}
            fill={n.hot ? 'var(--pink)' : n.color}
            opacity={0.55 + 0.45 * dim}
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
        )
      })}
      {visibleRepos.map((rep, idx) => {
        const a = (idx / Math.max(1, visibleRepos.length)) * Math.PI * 2
        const gx = cx + Math.cos(a) * 300
        const gy = cy + Math.sin(a) * 190
        return (
          <g key={rep.id}>
            <rect x={gx - 6} y={gy - 11} width={rep.id.length * 7 + 22} height={18}
              rx={9} fill="var(--bg-1)" opacity="0.8"
              stroke={rep.color} strokeOpacity="0.5" />
            <circle cx={gx + 3} cy={gy - 2} r={3} fill={rep.color} />
            <text x={gx + 11} y={gy + 3} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
      {projected.length === 0 && (
        <text x={cx} y={cy} textAnchor="middle" fill="var(--fg-3)"
          fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
    </svg>
  )
}

function hash(s: string): number {
  let h = 2166136261 >>> 0
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0 }
  return h
}
