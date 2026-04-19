'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import { computeDegree, groupByRepo, stableSortByDegreeDesc, seededRng, shortName } from './layout'

const MAX_PER_REPO = 32
const MAX_REPOS = 12

type Pillar = { gx: number; gy: number; elev: number; color: string; repo: string; node: GortexNode; hot: boolean }

export function ThreeDSkyline({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)

  const { pillars, visibleRepos } = useMemo(() => {
    if (!graph) return { pillars: [] as Pillar[], visibleRepos: [] as Repo[] }
    const degree = computeDegree(graph.nodes, graph.edges)
    const buckets = groupByRepo(graph.nodes)
    const visible = repos
      .filter((r) => !filterRepos.size || filterRepos.has(r.id))
      .filter((r) => (buckets.get(r.id)?.length ?? 0) > 0)
      .slice(0, MAX_REPOS)

    const maxDeg = Math.max(
      1,
      ...visible.flatMap((r) => (buckets.get(r.id) ?? []).map((n) => degree.get(n.id) ?? 0)),
    )
    const pillars: Pillar[] = []
    visible.forEach((rep, idx) => {
      const baseX = 180 + (idx % 4) * 220
      const baseY = 120 + Math.floor(idx / 4) * 260
      const rng = seededRng(hash(rep.id) + 23)
      const sorted = stableSortByDegreeDesc(buckets.get(rep.id) ?? [], degree).slice(0, MAX_PER_REPO)
      sorted.forEach((n) => {
        const deg = degree.get(n.id) ?? 0
        pillars.push({
          gx: baseX + (rng() - 0.5) * 160,
          gy: baseY + (rng() - 0.5) * 120,
          elev: 8 + (deg / maxDeg) * 62,
          color: rep.color,
          repo: rep.id,
          node: n,
          hot: deg >= Math.max(8, maxDeg * 0.6),
        })
      })
    })
    return { pillars, visibleRepos: visible }
  }, [graph, repos, filterRepos])

  const iso = (x: number, y: number, z: number) => ({
    x: 540 + (x - y) * 0.86,
    y: 280 + (x + y) * 0.5 - z,
  })

  return (
    <svg viewBox="0 0 1080 640" width="100%" height="100%">
      {Array.from({ length: 20 }, (_, i) => {
        const a = iso(i * 80, 0, 0)
        const b = iso(i * 80, 800, 0)
        return <line key={`gx${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.3" />
      })}
      {Array.from({ length: 20 }, (_, i) => {
        const a = iso(0, i * 80, 0)
        const b = iso(1600, i * 80, 0)
        return <line key={`gy${i}`} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="var(--line-1)" strokeWidth="0.3" />
      })}
      {visibleRepos.map((rep, idx) => {
        const baseX = 180 + (idx % 4) * 220
        const baseY = 120 + Math.floor(idx / 4) * 260
        const pad = 95
        const c = [
          [baseX - pad, baseY - pad],
          [baseX + pad, baseY - pad],
          [baseX + pad, baseY + pad],
          [baseX - pad, baseY + pad],
        ]
        const pts = c.map(([x, y]) => { const p = iso(x, y, 0); return `${p.x},${p.y}` }).join(' ')
        return (
          <polygon key={rep.id} points={pts} fill={rep.color} opacity="0.055"
            stroke={rep.color} strokeOpacity="0.35" strokeWidth="0.8" />
        )
      })}
      {[...pillars].sort((a, b) => a.gx + a.gy - (b.gx + b.gy)).map((n, i) => {
        const base = iso(n.gx, n.gy, 0)
        const top = iso(n.gx, n.gy, n.elev)
        return (
          <g key={i} style={{ cursor: 'pointer' }}
            onClick={() => setSym({
              id: n.node.id,
              kind: (n.node.kind as 'function') ?? 'function',
              name: n.node.name,
              repo: n.repo,
              file: n.node.file_path,
              sig: '', callers: 0, callees: 0, community: '', caveats: [],
            })}>
            <line x1={base.x} y1={base.y} x2={top.x} y2={top.y}
              stroke={n.hot ? 'var(--pink)' : n.color}
              strokeWidth={n.hot ? 3 : 2.4} strokeLinecap="round"
              opacity={n.hot ? 1 : 0.95} />
            <circle cx={top.x} cy={top.y} r={n.hot ? 3.6 : 3} fill={n.hot ? 'var(--pink)' : n.color} />
            <circle cx={base.x} cy={base.y} r={1.8} fill={n.color} opacity="0.35" />
          </g>
        )
      })}
      {visibleRepos.map((rep, idx) => {
        const baseX = 180 + (idx % 4) * 220
        const baseY = 120 + Math.floor(idx / 4) * 260
        const p = iso(baseX - 85, baseY + 95, 0)
        return (
          <g key={rep.id}>
            <rect x={p.x - 4} y={p.y - 10} width={rep.id.length * 6.5 + 18} height={16}
              fill="var(--bg-1)" opacity="0.75" rx={3} />
            <circle cx={p.x + 4} cy={p.y - 2} r={3} fill={rep.color} />
            <text x={p.x + 11} y={p.y + 2} fontFamily="JetBrains Mono" fontSize="10" fill="var(--fg-1)">
              {rep.id}
            </text>
          </g>
        )
      })}
      {pillars.length === 0 && (
        <text x={540} y={320} textAnchor="middle" fill="var(--fg-3)" fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
      {pillars.slice(0, 4).map((n, i) => {
        const top = iso(n.gx, n.gy, n.elev)
        return (
          <text key={`lbl${i}`} x={top.x + 6} y={top.y - 2} fontFamily="JetBrains Mono" fontSize="9"
            fill="var(--fg-2)" opacity="0.7">
            {shortName(n.node, 16)}
          </text>
        )
      })}
    </svg>
  )
}

function hash(s: string): number {
  let h = 2166136261 >>> 0
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0 }
  return h
}
