'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc, seededRng } from './layout'

const MAX_PLANES = 7
const MAX_PER_PLANE = 28

type PNode = { x: number; y: number; color: string; hot: boolean; id: string; node: GortexNode }
type Plane = {
  rep: Repo
  z: number
  corners: { x: number; y: number }[]
  nodes: PNode[]
  label: { x: number; y: number }
}

export function ThreeDStrata({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)

  const { planes, rain } = useMemo(() => {
    if (!graph) return { planes: [] as Plane[], rain: [] as { a: PNode; b: PNode; hot: boolean }[] }
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visible = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)
      .slice(0, MAX_PLANES)

    const iso = (x: number, y: number, z: number) => ({
      x: 540 + (x - y) * 0.82,
      y: 400 + (x + y) * 0.42 - z,
    })
    const planeW = 760
    const planeH = 260
    const planes: Plane[] = visible.map((rep, i) => {
      const z = (visible.length - 1 - i) * 70 + 40
      const ox = -planeW / 2
      const oy = -planeH / 2
      const corners = [
        [ox, oy], [ox + planeW, oy], [ox + planeW, oy + planeH], [ox, oy + planeH],
      ].map(([x, y]) => iso(x, y, z))
      const rng = seededRng(hash(rep.id) + 31)
      const sorted = stableSortByDegreeDesc(buckets.get(rep.id) ?? [], degree).slice(0, MAX_PER_PLANE)
      const maxDeg = Math.max(1, ...sorted.map((n) => degree.get(n.id) ?? 0))
      const nodes: PNode[] = sorted.map((n) => {
        const nx = (rng() - 0.5) * (planeW - 60)
        const ny = (rng() - 0.5) * (planeH - 40)
        const deg = degree.get(n.id) ?? 0
        return {
          ...iso(nx, ny, z),
          color: rep.color,
          hot: deg >= Math.max(6, maxDeg * 0.6),
          id: n.id,
          node: n,
        }
      })
      return { rep, z, corners, nodes, label: iso(ox + planeW + 8, oy + planeH - 12, z) }
    })

    const nodeIndex = new Map<string, { p: PNode; plane: number }>()
    planes.forEach((pl, pi) => pl.nodes.forEach((pn) => nodeIndex.set(pn.id, { p: pn, plane: pi })))
    const rain: { a: PNode; b: PNode; hot: boolean }[] = []
    for (const e of graph.edges) {
      if (!e.cross_repo) continue
      const a = nodeIndex.get(e.from)
      const b = nodeIndex.get(e.to)
      if (!a || !b || a.plane === b.plane) continue
      rain.push({ a: a.p, b: b.p, hot: e.kind === 'calls' })
      if (rain.length >= 60) break
    }

    return { planes, rain }
  }, [graph, repos, filterRepos])

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {planes.map(({ rep, corners, nodes, label }) => (
        <g key={rep.id}>
          <polygon
            points={corners.map((c) => `${c.x},${c.y}`).join(' ')}
            fill={rep.color} fillOpacity="0.07" stroke={rep.color}
            strokeOpacity="0.5" strokeWidth="0.8"
          />
          {nodes.map((n, j) => (
            <circle key={j} cx={n.x} cy={n.y} r={n.hot ? 3.2 : 2.2}
              fill={n.hot ? 'var(--pink)' : n.color} opacity="0.92"
              style={{ cursor: 'pointer' }}
              onClick={() => setSym({
                id: n.node.id,
                kind: (n.node.kind as 'function') ?? 'function',
                name: n.node.name,
                repo: rep.id,
                file: n.node.file_path,
                sig: '', callers: 0, callees: 0, community: '', caveats: [],
              })}
            />
          ))}
          <g>
            <rect x={label.x - 4} y={label.y - 12} width={rep.id.length * 7 + 20} height={18}
              rx={3} fill="var(--bg-1)" opacity="0.85" stroke={rep.color} strokeOpacity="0.4" />
            <circle cx={label.x + 4} cy={label.y - 3} r={3} fill={rep.color} />
            <text x={label.x + 12} y={label.y + 2} fontFamily="JetBrains Mono" fontSize="10.5" fill="var(--fg-1)">
              {rep.id}
            </text>
            <text x={label.x + 12} y={label.y + 14} fontFamily="JetBrains Mono" fontSize="9" fill="var(--fg-3)">
              {rep.nodes} · {rep.lang}
            </text>
          </g>
        </g>
      ))}
      {rain.map((e, i) => (
        <line key={i} x1={e.a.x} y1={e.a.y} x2={e.b.x} y2={e.b.y}
          stroke={e.hot ? 'var(--pink)' : 'var(--accent)'}
          strokeOpacity={e.hot ? 0.75 : 0.35}
          strokeWidth={e.hot ? 1.3 : 0.8}
          strokeDasharray={e.hot ? '0' : '2 3'} />
      ))}
      {planes.length === 0 && (
        <text x={540} y={320} textAnchor="middle" fill="var(--fg-3)" fontFamily="JetBrains Mono" fontSize="12">
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
