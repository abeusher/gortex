'use client'

import { useMemo } from 'react'
import { useInspector } from '@/lib/inspector'
import type { GraphData, GortexNode } from '@/lib/types'
import type { Repo } from '@/lib/schema'
import {
  computeDegree, groupByRepo, stableSortByDegreeDesc, kindColorVar,
} from './layout'

// Directory / file nesting depth in the rendered tree. Deeper nesting
// makes the layout unreadable, so we collapse to top-level package dir.
const MAX_DIRS_PER_REPO = 8
const MAX_FILES_PER_DIR = 6
const MAX_SYMBOLS_PER_FILE = 5
const MAX_VISIBLE_REPOS = 6

type TreeNode = {
  id: string
  label: string
  kind: 'root' | 'repo' | 'dir' | 'file' | 'symbol'
  color?: string
  node?: GortexNode
  children: TreeNode[]
  _d: number
  _x: number
  _id: number
  _w: number
}

function buildTree(graph: GraphData, repos: Repo[], filterRepos: Set<string>): TreeNode {
  const degree = computeDegree(graph.nodes, graph.edges)
  const buckets = groupByRepo(graph.nodes)
  const visible = repos.filter((r) => !filterRepos.size || filterRepos.has(r.id)).slice(0, MAX_VISIBLE_REPOS)

  const root: TreeNode = {
    id: 'root', label: 'workspace', kind: 'root', children: [],
    _d: 0, _x: 0.5, _id: 0, _w: 1,
  }

  for (const rep of visible) {
    const bucket = buckets.get(rep.id) ?? []
    if (bucket.length === 0) continue
    const repoNode: TreeNode = {
      id: `repo:${rep.id}`, label: rep.id, kind: 'repo',
      color: rep.color, children: [], _d: 0, _x: 0, _id: 0, _w: 1,
    }
    root.children.push(repoNode)

    // Group by top-level directory: the first path segment after the repo.
    const byDir = new Map<string, GortexNode[]>()
    for (const n of bucket) {
      const dir = topDir(n.file_path)
      const arr = byDir.get(dir) ?? []
      arr.push(n)
      byDir.set(dir, arr)
    }

    const dirs = [...byDir.entries()]
      .map(([d, ns]) => ({ d, ns, importance: ns.reduce((s, n) => s + (degree.get(n.id) ?? 0), 0) }))
      .sort((a, b) => b.importance - a.importance)
      .slice(0, MAX_DIRS_PER_REPO)

    for (const { d, ns } of dirs) {
      const dirNode: TreeNode = {
        id: `dir:${rep.id}:${d}`, label: d, kind: 'dir', color: rep.color,
        children: [], _d: 0, _x: 0, _id: 0, _w: 1,
      }
      repoNode.children.push(dirNode)

      const byFile = new Map<string, GortexNode[]>()
      for (const n of ns) {
        if (n.kind === 'file') continue
        const arr = byFile.get(n.file_path) ?? []
        arr.push(n)
        byFile.set(n.file_path, arr)
      }

      const files = [...byFile.entries()]
        .map(([f, syms]) => ({ f, syms, importance: syms.reduce((s, n) => s + (degree.get(n.id) ?? 0), 0) }))
        .sort((a, b) => b.importance - a.importance)
        .slice(0, MAX_FILES_PER_DIR)

      for (const { f, syms } of files) {
        const fileNode: TreeNode = {
          id: `file:${f}`, label: basename(f), kind: 'file', color: rep.color,
          children: [], _d: 0, _x: 0, _id: 0, _w: 1,
        }
        dirNode.children.push(fileNode)

        const symbols = stableSortByDegreeDesc(syms, degree).slice(0, MAX_SYMBOLS_PER_FILE)
        for (const s of symbols) {
          fileNode.children.push({
            id: s.id, label: s.name, kind: 'symbol', node: s,
            children: [], _d: 0, _x: 0, _id: 0, _w: 1,
          })
        }
      }
    }
  }
  return root
}

function topDir(path: string): string {
  // Strip trailing file, leaving the top-level directory under the repo.
  // Path already excludes the repo prefix (gortex uses repo-relative paths).
  if (!path) return '/'
  const parts = path.split('/')
  return parts.length > 1 ? parts[0] : '/'
}

function basename(path: string): string {
  const p = path.split('/')
  return p[p.length - 1] || path
}

function layoutTree(root: TreeNode): { nodes: TreeNode[]; edges: [TreeNode, TreeNode][] } {
  const nodes: TreeNode[] = []
  const edges: [TreeNode, TreeNode][] = []
  let idc = 0

  function computeWidth(n: TreeNode): number {
    if (n.children.length === 0) { n._w = 1; return 1 }
    n._w = n.children.reduce((s, c) => s + computeWidth(c), 0)
    return n._w
  }

  function place(n: TreeNode, depth: number, xStart: number, xEnd: number, parent: TreeNode | null) {
    n._d = depth
    n._x = (xStart + xEnd) / 2
    n._id = idc++
    nodes.push(n)
    if (parent) edges.push([parent, n])
    let cursor = xStart
    for (const c of n.children) {
      const span = ((xEnd - xStart) * c._w) / Math.max(1, n._w)
      place(c, depth + 1, cursor, cursor + span, n)
      cursor += span
    }
  }

  computeWidth(root)
  place(root, 0, 0, 1, null)
  return { nodes, edges }
}

export function GraphHierarchical({
  graph, repos, filterRepos,
}: {
  graph: GraphData | null
  repos: Repo[]
  filterRepos: Set<string>
}) {
  const setSym = useInspector((s) => s.setSym)
  const tree = useMemo(() => graph ? buildTree(graph, repos, filterRepos) : null, [graph, repos, filterRepos])
  const laid = useMemo(() => tree ? layoutTree(tree) : { nodes: [], edges: [] }, [tree])

  const W = 1080
  const H = 600
  const depth = laid.nodes.reduce((m, n) => Math.max(m, n._d), 0)
  const rowH = Math.max(70, Math.min(120, (H - 80) / Math.max(1, depth)))
  const pos = (n: TreeNode) => ({ x: n._x * (W - 140) + 70, y: 50 + n._d * rowH })

  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="100%">
      {laid.edges.map(([a, b], i) => {
        const p = pos(a); const q = pos(b)
        return (
          <path
            key={i}
            d={`M${p.x},${p.y + 14} C${p.x},${(p.y + q.y) / 2} ${q.x},${(p.y + q.y) / 2} ${q.x},${q.y - 14}`}
            fill="none"
            stroke="var(--line-2)"
            strokeWidth="1"
          />
        )
      })}
      {laid.nodes.map((n, i) => {
        if (n.kind === 'root') return null
        const p = pos(n)
        const tw = Math.max(70, n.label.length * 7 + 24)
        const fill = n.kind === 'repo' ? 'var(--bg-2)' : 'var(--bg-1)'
        const stroke = n.color ?? 'var(--line-2)'
        const accent = n.kind === 'symbol' ? kindColorVar(n.node?.kind) : n.color ?? 'var(--fg-2)'
        const clickable = n.kind === 'symbol' && n.node
        return (
          <g
            key={i}
            transform={`translate(${p.x - tw / 2},${p.y - 14})`}
            style={{ cursor: clickable ? 'pointer' : 'default' }}
            onClick={() => {
              if (!clickable) return
              const nd = n.node!
              setSym({
                id: nd.id,
                kind: (nd.kind as 'function') ?? 'function',
                name: nd.name,
                repo: nd.repo_prefix ?? '',
                file: nd.file_path,
                sig: '',
                callers: 0,
                callees: 0,
                community: '',
                caveats: [],
              })
            }}
          >
            <rect
              width={tw}
              height={28}
              rx={5}
              fill={fill}
              stroke={stroke}
              strokeOpacity={n.kind === 'symbol' ? 0.55 : 0.7}
              strokeWidth={n.kind === 'repo' ? 1.4 : 1}
            />
            <circle cx={12} cy={14} r={4} fill={accent} />
            <text x={22} y={18} fontFamily="JetBrains Mono" fontSize="11" fill="var(--fg-0)">
              {n.label.length > 24 ? n.label.slice(0, 23) + '…' : n.label}
            </text>
          </g>
        )
      })}
      {laid.nodes.length <= 1 && (
        <text x={W / 2} y={H / 2} textAnchor="middle" fill="var(--fg-3)" fontFamily="JetBrains Mono" fontSize="12">
          No graph data — run `gortex index .` to populate.
        </text>
      )}
    </svg>
  )
}
