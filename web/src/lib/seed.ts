/* Gortex — intentional design-only fixtures.
 *
 * EVERYTHING IN THIS FILE IS A DOCUMENTED MOCK. The frontend should
 * never read real-time graph data from here — use `lib/hooks.ts` and
 * the `/v1/*` endpoints instead. The seeds below remain because the
 * server has no data source for them yet:
 *
 *  - FAKE_SPARK     — per-repo sparkline series. Requires a time-series
 *                     store of repo size over time, which the indexer
 *                     does not maintain. Tracked in web/AGENTS.md.
 *
 *  - INVESTIGATION_FLOW / INVESTIGATION_NOTES — there is no
 *                     `/v1/investigations` endpoint yet. Investigations
 *                     are user-authored entities (a hypothesis + pinned
 *                     flow steps + notes) and need persistent storage.
 *                     The Investigation page renders this static demo
 *                     so the IA stays discoverable until that lands.
 *
 *  - INVESTIGATION_SOURCE_PEEK — synthetic source snippet shown beside
 *                     the flow trace; depends on which step the user
 *                     selects, which we cannot resolve without a real
 *                     investigation entity.
 *
 * Recent searches are no longer here — they moved to localStorage
 * (see `useRecentSearches` in lib/hooks.ts).
 *
 * Graph views (/graph) now consume real data via `useGraph()` and the
 * `/v1/graph` endpoint. No rnd-seeded nodes/edges remain.
 *
 * If you find anything else mocked outside this file, it is a bug —
 * file an issue or wire it to /v1/*.
 */

export type FakeSparkSource = Record<string, number[]>
export const FAKE_SPARK: FakeSparkSource = {
  // Falls back to a flat line for any repo not listed, so this never
  // hard-codes the design's repo names; the dashboard sparkline renders
  // a neutral series until time-series data exists.
  default: [3, 4, 4, 5, 5, 5, 6, 6, 6, 7, 7, 8],
}

export type InvestigationStep = {
  idx: number
  repo: string
  where: string
  what: string
  caveat?: string
  risk?: boolean
}

// MOCK: see header — needs /v1/investigations.
export const INVESTIGATION_FLOW: InvestigationStep[] = [
  { idx: 1,  repo: 'web',          where: 'pages/email.tsx:sendEmail',                what: 'POST /ingest/email with raw payload' },
  { idx: 2,  repo: 'core-api',     where: 'internal/http/handler.go:RegisterRoutes',  what: 'route → EmailIngestHandler' },
  { idx: 3,  repo: 'core-api',     where: 'internal/middleware/auth.go:Authn',        what: 'auth check · hot path', caveat: 'hot' },
  { idx: 4,  repo: 'core-api',     where: 'internal/ingest/email.go:EmailIngestHandler', what: 'parse headers + body' },
  { idx: 5,  repo: 'core-api',     where: 'internal/link/extractor.go:ExtractLinks',  what: 'find URLs', caveat: 'deprecated' },
  { idx: 6,  repo: 'core-api',     where: 'internal/store/postgres.go:Insert',        what: 'write inbound email row', risk: true },
  { idx: 7,  repo: 'core-api',     where: 'internal/events/publish.go:Emit',          what: 'publish link.Extracted' },
  { idx: 8,  repo: 'email-worker', where: 'internal/handler.go:OnExtracted',          what: 'fetch preview metadata' },
  { idx: 9,  repo: 'worker',       where: 'internal/notifier.go:PushTuckUpdated',     what: 'emit push.TuckUpdated' },
  { idx: 10, repo: 'tuck_app',     where: 'features/sync/listener.dart:onPush',       what: 'client sync receives update' },
]

// MOCK: see header — investigation hypothesis and pinned source peek.
export const INVESTIGATION_NOTES = {
  hypothesis:
    '500s correlate with recent verifyJWT cache TTL drop from 5m → 30s. Combined with ExtractLinks running synchronously on the request path, auth cache misses pile up when the link extractor backs off.',
  next:
    '(1) move link extraction to worker via link.Extracted; (2) bump cache TTL back; (3) add boundary guard so UI can\'t import internal again.',
}

// MOCK: see header — sample source peek; depends on the synthetic flow.
export const INVESTIGATION_SOURCE_PEEK = `28 // Authn validates the bearer token on every request.
29 // Note: this is on the hot path — 83% of incidents touch it.
30 func Authn(next http.Handler) http.Handler {
31   return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
32     tok := r.Header.Get("Authorization")
33     if tok == "" {
34       writeError(w, 401, "missing token")
35       return
36     }
37     claims, err := verifyJWT(tok)
38     if err != nil {
39       writeError(w, 401, err.Error())
40       return
41     }`

// MOCK: see header — synthetic recent-edits list for the investigation
// timeline tile. Real git history would come from /v1/changes (TBD).
export const INVESTIGATION_TIMELINE = [
  { t: '2d ago',  who: '@sam',  msg: 'move verifyJWT to middleware/auth',           hash: 'c41bd2a' },
  { t: '3d ago',  who: '@ira',  msg: 'add TTL cache to verifyJWT',                  hash: '98a12e1' },
  { t: '5d ago',  who: '@sam',  msg: 'ExtractLinks: normalize UTM stripping',       hash: '1e904fa' },
  { t: '9d ago',  who: '@mike', msg: 'rename InboundEmail → IngressEmail',          hash: 'fe7729c', risk: true },
  { t: '12d ago', who: '@ira',  msg: 'add boundary guard: web ↛ core-api/internal', hash: '4b2cc1f' },
]
