'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc } from './layout'

const MAX_REPOS = 12

type Building = {
  x0: number; y0: number; x1: number; y1: number; h: number
  color: string; hot: boolean; node: GortexNode
}
type District = { rep: Repo; ox: number; oy: number; w: number; h: number; buildings: Building[] }

export function ThreeDCity({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)

  const districts = useMemo<District[]>(() => {
    if (!graph) return []
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visible = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)
      .slice(0, MAX_REPOS)

    return visible.map((rep, idx) => {
      const col = idx % 4
      const row = Math.floor(idx / 4)
      const ox = -440 + col * 230
      const oy = -220 + row * 260
      const w = 210
      const h = 230
      const cols = 6
      const rows = 5
      const capacity = cols * rows
      const sorted = stableSortByDegreeDesc(buckets.get(rep.id) ?? [], degree).slice(0, capacity)
      const maxDeg = Math.max(1, ...sorted.map((n) => degree.get(n.id) ?? 0))
      const bw = w / cols
      const bh = h / rows
      const buildings: Building[] = sorted.map((n, k) => {
        const i = k % cols
        const j = Math.floor(k / cols)
        const deg = degree.get(n.id) ?? 0
        const footprint = 0.6 + Math.min(0.35, (deg / maxDeg) * 0.35)
        const height = 12 + (deg / maxDeg) * 70
        const pad = ((1 - footprint) * bw) / 2
        return {
          x0: ox + i * bw + pad,
          y0: oy + j * bh + pad,
          x1: ox + (i + 1) * bw - pad,
          y1: oy + (j + 1) * bh - pad,
          h: height,
          color: rep.color,
          hot: deg >= Math.max(8, maxDeg * 0.6),
          node: n,
        }
      })
      return { rep, ox, oy, w, h, buildings }
    })
  }, [graph, repos, filterRepos])

  const iso = (x: number, y: number, z: number) => ({
    x: 540 + (x - y) * 0.82,
    y: 340 + (x + y) * 0.46 - z,
  })

  function drawBox(b: Building, i: number, repoId: string) {
    const t1 = iso(b.x0, b.y0, b.h)
    const t2 = iso(b.x1, b.y0, b.h)
    const t3 = iso(b.x1, b.y1, b.h)
    const t4 = iso(b.x0, b.y1, b.h)
    const r1 = iso(b.x1, b.y0, 0)
    const r2 = iso(b.x1, b.y1, 0)
    const f1 = iso(b.x0, b.y1, 0)
    const f2 = iso(b.x1, b.y1, 0)
    const strokeC = b.hot ? 'var(--pink)' : b.color
    return (
      <g key={i} style={{ cursor: 'pointer' }}
        onClick={() => setSym({
          id: b.node.id,
          kind: (b.node.kind as 'function') ?? 'function',
          name: b.node.name,
          repo: repoId,
          file: b.node.file_path,
          sig: '', callers: 0, callees: 0, community: '', caveats: [],
        })}>
        <polygon points={`${t2.x},${t2.y} ${t3.x},${t3.y} ${r2.x},${r2.y} ${r1.x},${r1.y}`}
          fill={b.color} fillOpacity="0.22" stroke={strokeC} strokeOpacity="0.7" strokeWidth="0.5" />
        <polygon points={`${t4.x},${t4.y} ${t3.x},${t3.y} ${f2.x},${f2.y} ${f1.x},${f1.y}`}
          fill={b.color} fillOpacity="0.32" stroke={strokeC} strokeOpacity="0.7" strokeWidth="0.5" />
        <polygon points={`${t1.x},${t1.y} ${t2.x},${t2.y} ${t3.x},${t3.y} ${t4.x},${t4.y}`}
          fill={b.hot ? 'var(--pink)' : b.color} fillOpacity={b.hot ? 0.6 : 0.45}
          stroke={strokeC} strokeOpacity="0.9" strokeWidth="0.6" />
      </g>
    )
  }

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {Array.from({ length: 16 }, (_, i) => {
        const a = iso(-500 + i * 70, -300, 0)
        const b = iso(-500 + i * 70, 500, 0)
        return <line key={`gx${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.25" />
      })}
      {Array.from({ length: 16 }, (_, i) => {
        const a = iso(-600, -300 + i * 70, 0)
        const b = iso(600, -300 + i * 70, 0)
        return <line key={`gy${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.25" />
      })}
      {[...districts].sort((a, b) => a.ox + a.oy - (b.ox + b.oy)).map((d) => {
        const c = [
          [d.ox - 8, d.oy - 8], [d.ox + d.w + 8, d.oy - 8],
          [d.ox + d.w + 8, d.oy + d.h + 8], [d.ox - 8, d.oy + d.h + 8],
        ]
        const pts = c.map(([x, y]) => { const p = iso(x, y, 0); return `${p.x},${p.y}` }).join(' ')
        const label = iso(d.ox, d.oy - 8, 0)
        return (
          <g key={d.rep.id}>
            <polygon points={pts} fill={d.rep.color} fillOpacity="0.06"
              stroke={d.rep.color} strokeOpacity="0.5" strokeWidth="0.8" />
            {[...d.buildings].sort((a, b) => a.x0 + a.y0 - (b.x0 + b.y0)).map((b, i) => drawBox(b, i, d.rep.id))}
            <g>
              <circle cx={label.x} cy={label.y - 4} r={3} fill={d.rep.color} />
              <text x={label.x + 8} y={label.y - 0.5} fontFamily="JetBrains Mono" fontSize="10.5" fill="var(--fg-1)">
                {d.rep.id}
              </text>
            </g>
          </g>
        )
      })}
      {districts.length === 0 && (
        <text x={540} y={340} textAnchor="middle" fill="var(--fg-3)"
          fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
    </svg>
  )
}
