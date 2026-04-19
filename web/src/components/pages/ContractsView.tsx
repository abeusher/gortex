'use client'

import { useMemo, useState } from 'react'
import { Icon } from '@/components/primitives/Icon'
import { CaveatBadge } from '@/components/primitives/Caveat'
import { CodeBlock } from '@/components/primitives/CodeBlock'
import { useContracts, useSymbolSource } from '@/lib/hooks'
import type {
  Contract,
  ContractLocation,
  ContractScope,
  ContractType,
} from '@/lib/schema'

type ScopeFilter = ContractScope | 'all'
type TypeFilter = ContractType | 'all'

const TYPE_FILTERS: { value: TypeFilter; label: string }[] = [
  { value: 'all', label: 'All types' },
  { value: 'http', label: 'HTTP' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'graphql', label: 'GraphQL' },
  { value: 'topic', label: 'Topic' },
  { value: 'ws', label: 'WS' },
  { value: 'env', label: 'Env' },
  { value: 'openapi', label: 'OpenAPI' },
  { value: 'dependency', label: 'Dep' },
]

const SCOPE_FILTERS: { value: ScopeFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'own', label: 'Own' },
  { value: 'external', label: 'External' },
]

export function ContractsView() {
  const { data, loading, error, refetch } = useContracts()
  const contracts = data ?? []

  const [scope, setScope] = useState<ScopeFilter>('all')
  const [typ, setTyp] = useState<TypeFilter>('all')
  const [openId, setOpenId] = useState<string | null>(null)

  const typeCounts = useMemo(() => countBy(contracts, (c) => c.type), [contracts])
  const scopeCounts = useMemo(() => countBy(contracts, (c) => c.scope), [contracts])

  const filtered = useMemo(
    () =>
      contracts.filter(
        (c) => (scope === 'all' || c.scope === scope) && (typ === 'all' || c.type === typ),
      ),
    [contracts, scope, typ],
  )
  const breaking = filtered.filter((c) => c.breaking).length

  return (
    <>
      <div className="page-hd">
        <div>
          <h1>Contracts</h1>
          <div className="sub">
            {loading
              ? 'Loading detected contracts…'
              : `${filtered.length} of ${contracts.length} API/event boundaries · ${breaking} breaking`}
          </div>
        </div>
        <div className="actions">
          <button type="button" className="btn" onClick={refetch}>
            <Icon name="history" size={12} /> Refresh
          </button>
        </div>
      </div>

      <div
        style={{
          display: 'flex',
          gap: 16,
          padding: '12px 22px 0',
          flexWrap: 'wrap',
          alignItems: 'center',
        }}
      >
        <div className="hstack" style={{ gap: 0, border: '1px solid var(--line)', borderRadius: 6, overflow: 'hidden' }}>
          {SCOPE_FILTERS.map((s) => {
            const active = scope === s.value
            const count = s.value === 'all' ? contracts.length : scopeCounts.get(s.value) ?? 0
            return (
              <button
                key={s.value}
                type="button"
                onClick={() => setScope(s.value)}
                style={{
                  padding: '6px 12px',
                  fontSize: 12,
                  border: 'none',
                  borderRight: '1px solid var(--line)',
                  background: active ? 'var(--bg-1)' : 'transparent',
                  color: active ? 'var(--fg-0)' : 'var(--fg-2)',
                  cursor: 'pointer',
                  fontWeight: active ? 600 : 400,
                }}
              >
                {s.label}
                <span className="faint" style={{ marginLeft: 6 }}>{count}</span>
              </button>
            )
          })}
        </div>

        <div className="hstack" style={{ gap: 6, flexWrap: 'wrap' }}>
          {TYPE_FILTERS.map((t) => {
            const count = t.value === 'all' ? contracts.length : typeCounts.get(t.value) ?? 0
            if (t.value !== 'all' && count === 0) return null
            const active = typ === t.value
            return (
              <button
                key={t.value}
                type="button"
                onClick={() => setTyp(t.value)}
                className="chip"
                style={{
                  cursor: 'pointer',
                  background: active ? 'var(--bg-1)' : 'transparent',
                  color: active ? 'var(--fg-0)' : 'var(--fg-2)',
                  borderColor: active ? 'var(--fg-2)' : 'var(--line)',
                  fontWeight: active ? 600 : 400,
                }}
              >
                {t.label} <span className="faint" style={{ marginLeft: 4 }}>{count}</span>
              </button>
            )
          })}
        </div>
      </div>

      {error && (
        <div style={{ padding: 22, color: 'var(--danger)', fontSize: 13 }}>
          Failed to load contracts: {error}
        </div>
      )}

      {!error && contracts.length === 0 && !loading && (
        <div style={{ padding: 22, color: 'var(--fg-2)', fontSize: 13 }}>
          No contracts detected. Make sure the indexer ran on a repository that exposes HTTP, gRPC, or event topics.
        </div>
      )}

      {!error && contracts.length > 0 && filtered.length === 0 && (
        <div style={{ padding: 22, color: 'var(--fg-2)', fontSize: 13 }}>
          No contracts match the current filters.
        </div>
      )}

      {filtered.length > 0 && (
        <div style={{ padding: '18px 22px', overflow: 'auto' }}>
          <div style={{ display: 'grid', gap: 10 }}>
            {filtered.map((c) => (
              <ContractRow
                key={c.id}
                c={c}
                expanded={openId === c.id}
                onToggle={() => setOpenId(openId === c.id ? null : c.id)}
              />
            ))}
          </div>
        </div>
      )}
    </>
  )
}

function ContractRow({
  c,
  expanded,
  onToggle,
}: {
  c: Contract
  expanded: boolean
  onToggle: () => void
}) {
  const badge = kindBadge(c.kind)
  const [selected, setSelected] = useState<ContractLocation | null>(null)
  const [mode, setMode] = useState<'source' | 'schema'>('source')
  const providerLoc = c.locations.find((l) => l.role === 'provider') ?? null

  const openTrace = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!expanded) onToggle()
    setMode('source')
    setSelected(providerLoc ?? c.locations[0] ?? null)
  }
  const openSchema = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!expanded) onToggle()
    setMode('schema')
    if (!selected) setSelected(providerLoc ?? c.locations[0] ?? null)
  }

  return (
    <div className="card">
      <div
        onClick={onToggle}
        style={{
          display: 'grid',
          gridTemplateColumns: '28px 1fr auto',
          gap: 14,
          padding: 14,
          alignItems: 'center',
          cursor: 'pointer',
        }}
      >
        <div
          style={{
            width: 28,
            height: 28,
            borderRadius: 6,
            background: badge.bg,
            color: badge.fg,
            display: 'grid',
            placeItems: 'center',
            fontFamily: 'JetBrains Mono',
            fontSize: 10,
            fontWeight: 600,
          }}
        >
          {badge.label}
        </div>
        <div>
          <div className="hstack" style={{ gap: 8, flexWrap: 'wrap' }}>
            <Icon name={expanded ? 'caretdn' : 'caret'} size={10} />
            <span className="mono" style={{ fontSize: 14, color: 'var(--fg-0)' }}>{c.name}</span>
            <span className="chip" title={`type: ${c.type}`}>{c.type}</span>
            <span
              className="chip"
              title={c.scope === 'own' ? 'Defined in this project' : 'External or consumed-only'}
              style={{
                color: c.scope === 'own' ? 'var(--fg-0)' : 'var(--fg-2)',
                borderColor: c.scope === 'own' ? 'var(--fg-2)' : 'var(--line)',
              }}
            >
              {c.scope}
            </span>
            {c.breaking && <CaveatBadge kind="boundary" />}
            {c.version && <span className="chip">{c.version}</span>}
          </div>
          <div className="hstack" style={{ gap: 10, marginTop: 6, fontSize: 11.5, color: 'var(--fg-2)', flexWrap: 'wrap' }}>
            <span>
              Produced by <span className="tag-dim">{c.producer || 'unknown'}</span>
            </span>
            {c.consumers.length > 0 && (
              <>
                <span>→</span>
                <span className="hstack" style={{ gap: 4 }}>
                  consumed by{' '}
                  {c.consumers.map((r) => (
                    <span key={r} className="tag-dim">{r}</span>
                  ))}
                </span>
              </>
            )}
            <span className="faint">· {c.locations.length} location{c.locations.length === 1 ? '' : 's'}</span>
          </div>
        </div>
        <div className="hstack" style={{ gap: 6 }}>
          <button type="button" className="btn small ghost" onClick={openTrace}>
            <Icon name="graph" size={11} /> Trace
          </button>
          <button type="button" className="btn small" onClick={openSchema}>
            <Icon name="file" size={11} /> Schema
          </button>
        </div>
      </div>

      {expanded && (
        <ContractDetail
          c={c}
          selected={selected}
          onSelect={setSelected}
          mode={mode}
          setMode={setMode}
        />
      )}
    </div>
  )
}

function ContractDetail({
  c,
  selected,
  onSelect,
  mode,
  setMode,
}: {
  c: Contract
  selected: ContractLocation | null
  onSelect: (l: ContractLocation) => void
  mode: 'source' | 'schema'
  setMode: (m: 'source' | 'schema') => void
}) {
  const providers = c.locations.filter((l) => l.role === 'provider')
  const consumers = c.locations.filter((l) => l.role === 'consumer')

  return (
    <div
      style={{
        borderTop: '1px solid var(--line)',
        display: 'grid',
        gridTemplateColumns: 'minmax(240px, 320px) 1fr',
        minHeight: 220,
      }}
    >
      <div
        style={{
          borderRight: '1px solid var(--line)',
          padding: '10px 12px',
          maxHeight: 480,
          overflow: 'auto',
          fontSize: 12,
        }}
      >
        {providers.length > 0 && (
          <LocationGroup
            label="Providers"
            locations={providers}
            selected={selected}
            onSelect={onSelect}
          />
        )}
        {consumers.length > 0 && (
          <LocationGroup
            label="Consumers"
            locations={consumers}
            selected={selected}
            onSelect={onSelect}
          />
        )}
        {c.locations.length === 0 && (
          <div className="faint" style={{ padding: 8 }}>No locations recorded.</div>
        )}
      </div>

      <div style={{ padding: '10px 12px', display: 'grid', gridTemplateRows: 'auto 1fr', minHeight: 0 }}>
        <div className="hstack" style={{ gap: 6, marginBottom: 8 }}>
          <button
            type="button"
            className="chip"
            onClick={() => setMode('source')}
            style={{
              cursor: 'pointer',
              background: mode === 'source' ? 'var(--bg-1)' : 'transparent',
              color: mode === 'source' ? 'var(--fg-0)' : 'var(--fg-2)',
              borderColor: mode === 'source' ? 'var(--fg-2)' : 'var(--line)',
              fontWeight: mode === 'source' ? 600 : 400,
            }}
          >
            Source
          </button>
          <button
            type="button"
            className="chip"
            onClick={() => setMode('schema')}
            style={{
              cursor: 'pointer',
              background: mode === 'schema' ? 'var(--bg-1)' : 'transparent',
              color: mode === 'schema' ? 'var(--fg-0)' : 'var(--fg-2)',
              borderColor: mode === 'schema' ? 'var(--fg-2)' : 'var(--line)',
              fontWeight: mode === 'schema' ? 600 : 400,
            }}
          >
            Schema / Meta
          </button>
          {selected && (
            <span className="faint mono" style={{ marginLeft: 'auto', fontSize: 11 }}>
              {selected.file_path}:{selected.line}
            </span>
          )}
        </div>

        {mode === 'source' ? (
          <SourcePane symbolId={selected?.symbol_id ?? null} filePath={selected?.file_path ?? null} />
        ) : (
          <SchemaPane loc={selected} />
        )}
      </div>
    </div>
  )
}

function LocationGroup({
  label,
  locations,
  selected,
  onSelect,
}: {
  label: string
  locations: ContractLocation[]
  selected: ContractLocation | null
  onSelect: (l: ContractLocation) => void
}) {
  const byRepo = new Map<string, ContractLocation[]>()
  for (const l of locations) {
    const key = l.repo_prefix || '(unknown)'
    const bucket = byRepo.get(key) ?? []
    bucket.push(l)
    byRepo.set(key, bucket)
  }
  return (
    <div style={{ marginBottom: 12 }}>
      <div className="faint" style={{ textTransform: 'uppercase', fontSize: 10, letterSpacing: 0.5, marginBottom: 6 }}>
        {label} · {locations.length}
      </div>
      {[...byRepo.entries()].map(([repo, items]) => (
        <div key={repo} style={{ marginBottom: 8 }}>
          <div className="tag-dim" style={{ marginBottom: 4 }}>{repo}</div>
          <div style={{ display: 'grid', gap: 2 }}>
            {items.map((l, i) => {
              const isSel = selected === l
              return (
                <button
                  key={`${l.file_path}:${l.line}:${i}`}
                  type="button"
                  onClick={() => onSelect(l)}
                  className="mono"
                  title={l.symbol_id}
                  style={{
                    textAlign: 'left',
                    background: isSel ? 'var(--bg-1)' : 'transparent',
                    color: isSel ? 'var(--fg-0)' : 'var(--fg-2)',
                    border: '1px solid',
                    borderColor: isSel ? 'var(--fg-2)' : 'transparent',
                    borderRadius: 4,
                    padding: '3px 6px',
                    fontSize: 11,
                    cursor: 'pointer',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {l.file_path}:{l.line}
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}

function SourcePane({ symbolId, filePath }: { symbolId: string | null; filePath: string | null }) {
  const { data, loading, error } = useSymbolSource(symbolId)
  if (!symbolId) {
    return (
      <div className="faint" style={{ padding: 12, fontSize: 12 }}>
        Select a location on the left to view its source.
      </div>
    )
  }
  if (loading) return <div className="faint" style={{ padding: 12 }}>Loading source…</div>
  if (error) return <div style={{ padding: 12, color: 'var(--danger)', fontSize: 12 }}>Failed to load source: {error}</div>
  if (!data) return <div className="faint" style={{ padding: 12, fontSize: 12 }}>No source available for {symbolId}.</div>
  return <CodeBlock code={data} filePath={filePath ?? undefined} maxHeight={420} />
}

function SchemaPane({ loc }: { loc: ContractLocation | null }) {
  if (!loc) {
    return (
      <div className="faint" style={{ padding: 12, fontSize: 12 }}>
        Select a location on the left to inspect its schema / meta.
      </div>
    )
  }
  const meta = loc.meta ?? {}
  const hasMeta = Object.keys(meta).length > 0
  return (
    <div style={{ display: 'grid', gap: 8 }}>
      <div className="hstack" style={{ gap: 8, flexWrap: 'wrap', fontSize: 11.5 }}>
        <span className="chip">{loc.role}</span>
        <span className="chip">{loc.repo_prefix || '(unknown)'}</span>
        {loc.symbol_id && <span className="mono faint">{loc.symbol_id}</span>}
      </div>
      {hasMeta ? (
        <CodeBlock code={JSON.stringify(meta, null, 2)} lang="json" maxHeight={380} />
      ) : (
        <div className="faint" style={{ padding: 12, fontSize: 12 }}>
          No schema metadata attached to this location.
        </div>
      )}
    </div>
  )
}

function countBy<T, K>(xs: T[], key: (x: T) => K): Map<K, number> {
  const m = new Map<K, number>()
  for (const x of xs) m.set(key(x), (m.get(key(x)) ?? 0) + 1)
  return m
}

function kindBadge(kind: string): { label: string; bg: string; fg: string } {
  switch (kind) {
    case 'EVENT':
      return { label: 'EV', bg: 'oklch(0.78 0.14 300 / 0.18)', fg: 'var(--violet)' }
    case 'URL':
      return { label: 'URL', bg: 'oklch(0.82 0.15 80 / 0.18)', fg: 'var(--warn)' }
    case 'ENV':
      return { label: 'ENV', bg: 'oklch(0.8 0.1 140 / 0.18)', fg: 'var(--ok)' }
    case 'DEP':
      return { label: 'DEP', bg: 'oklch(0.7 0.08 260 / 0.18)', fg: 'var(--fg-2)' }
    default:
      return { label: 'API', bg: 'oklch(0.82 0.14 45 / 0.18)', fg: 'var(--k-contract)' }
  }
}
