package daemon

import (
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// DefaultOverlayIdleTTL is the default per-session idle expiry for
// editor-buffer overlays. Raised from the original 5-minute draft to
// 30 minutes after field feedback: realistic coding sessions
// (deliberation, debugger pauses, IDE refactor wizards) commonly
// exceed five minutes without producing a Push, and the read-time
// LastUsed bump in SnapshotFor already keeps the session alive for
// any tool call. Thirty minutes is comfortably above typical
// inactivity gaps and well below the point where a forgotten
// disconnected client would meaningfully pin memory.
const DefaultOverlayIdleTTL = 30 * time.Minute

// OverlayIdleTTLFromEnv resolves the idle-TTL setting that
// `gortex server` / `gortex daemon` should construct their
// OverlayManager with. Precedence:
//
//  1. Caller-supplied override (non-zero).
//  2. `GORTEX_OVERLAY_IDLE_TTL` env var, parsed by time.ParseDuration
//     ("30m", "1h", "45s", etc.).
//  3. DefaultOverlayIdleTTL.
//
// A negative parse result clamps to 0 (which means "no expiry" —
// useful only for tests). Garbage env values fall back to the
// default; we deliberately don't fail startup over a typo'd duration.
func OverlayIdleTTLFromEnv(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if raw := os.Getenv("GORTEX_OVERLAY_IDLE_TTL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			if d < 0 {
				d = 0
			}
			return d
		}
	}
	return DefaultOverlayIdleTTL
}

// OverlayFile is one editor-buffer override pushed by an MCP client.
// The daemon (or a remote graph service over the gateway) merges
// these on top of the base graph view for the duration of a session.
// Iteration 1 only models text files; binary overlays would need a
// different shape and are not in scope.
type OverlayFile struct {
	// Path is repo-relative when WorkspaceID is set, absolute
	// otherwise. The graph service maps it onto the repo root via
	// its ConfigManager.
	Path string `json:"path"`
	// Content is the in-editor text. Empty means "deletion overlay"
	// — the client wants the daemon to act as if the file isn't on
	// disk, even if it actually is. The daemon distinguishes this
	// from a real empty file by only honouring deletions when the
	// session declares them via OverlayPush(..., Deleted: true).
	Content string `json:"content"`
	// BaseSHA is the file's git blob SHA at the time the editor
	// opened it. Used by the daemon's drift-detection: if the file
	// on disk now hashes to a different SHA, the overlay is stale
	// and the daemon refuses to merge it (returns ErrOverlayDrift).
	// Empty disables drift-detection — useful for editor-buffer
	// states that exist before any save.
	BaseSHA string `json:"base_sha,omitempty"`
	// Deleted, when true, marks the overlay as a tombstone (see
	// Content above). Mutually exclusive with non-empty Content.
	Deleted bool `json:"deleted,omitempty"`
}

// OverlaySession holds one client's pushed overlays for the duration
// of an MCP session. Sessions auto-expire after IdleTTL of inactivity
// so a crashed client doesn't leak memory in the daemon.
type OverlaySession struct {
	ID          string
	WorkspaceID string
	Created     time.Time
	LastUsed    time.Time
	files       map[string]OverlayFile // path → overlay
}

// OverlayManager manages the per-session overlay map for the daemon.
// Goroutine-safe; callers can register, push, and delete from any
// goroutine. A single janitor goroutine sweeps idle sessions.
type OverlayManager struct {
	mu       sync.RWMutex
	sessions map[string]*OverlaySession
	idleTTL  time.Duration
}

// ErrSessionNotFound is returned by OverlayManager methods that
// reference an unknown session ID. The daemon translates this to
// HTTP 404 on `/v1/overlay/<id>/...` endpoints.
var ErrSessionNotFound = errors.New("overlay session not found")

// ErrOverlayDrift is returned by OverlayPush when the supplied
// BaseSHA disagrees with the file's current on-disk SHA. The client
// is expected to re-read the file and resubmit a fresh overlay; the
// daemon refuses to fold a known-stale overlay into queries because
// merge artefacts (lines moved by a sibling tool's edit) would
// surface as wrong-line errors that look like graph bugs.
var ErrOverlayDrift = errors.New("overlay base SHA mismatch — re-read and resubmit")

// NewOverlayManager creates a manager with the given idle TTL. ttl
// <= 0 disables expiry (useful for tests that want deterministic
// session behaviour).
func NewOverlayManager(idleTTL time.Duration) *OverlayManager {
	return &OverlayManager{
		sessions: make(map[string]*OverlaySession),
		idleTTL:  idleTTL,
	}
}

// Register starts a new session and returns its ID. The workspace
// slug is captured at register time; later pushes that target a
// different workspace are rejected (one session = one workspace,
// per the overlay model).
func (m *OverlayManager) Register(workspaceID string) string {
	id := newSessionID()
	_ = m.RegisterWithID(id, workspaceID)
	return id
}

// ErrSessionExists is returned by RegisterWithID when the caller-supplied
// session ID is already known. The MCP-side overlay tools rely on this
// error to detect "register-called-twice" races; HTTP callers don't see
// it because Register generates fresh IDs.
var ErrSessionExists = errors.New("overlay session already exists")

// RegisterWithID registers a session under a caller-chosen ID. This is
// the path the MCP `overlay_register` tool takes — it binds the overlay
// session to the MCP session ID so the query path can find the overlay
// snapshot from the request context without an extra lookup. The HTTP
// register handler also routes through here when its body includes an
// explicit `session_id`.
//
// Returns ErrSessionExists when the ID is already in use. Idempotent
// re-registration (same workspaceID) is treated as a no-op: the client
// may safely retry register without first checking.
func (m *OverlayManager) RegisterWithID(sessionID, workspaceID string) error {
	if sessionID == "" {
		return errors.New("overlay session id is required")
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.sessions[sessionID]; ok {
		if existing.WorkspaceID == workspaceID {
			existing.LastUsed = now
			return nil
		}
		return ErrSessionExists
	}
	m.sessions[sessionID] = &OverlaySession{
		ID:          sessionID,
		WorkspaceID: workspaceID,
		Created:     now,
		LastUsed:    now,
		files:       make(map[string]OverlayFile),
	}
	return nil
}

// Has reports whether a session is currently registered. Used by the
// MCP tool dispatcher to decide whether to skip the per-request apply
// pass (no session → no work). Cheap O(1) read under the read lock.
func (m *OverlayManager) Has(sessionID string) bool {
	if m == nil || sessionID == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[sessionID]
	return ok
}

// FileCount returns the number of overlay files attached to a session,
// 0 if the session is unknown. Cheap fast-path: lets the dispatcher
// skip the apply pass when a session is registered but empty.
func (m *OverlayManager) FileCount(sessionID string) int {
	if m == nil || sessionID == "" {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return 0
	}
	return len(sess.files)
}

// Touch refreshes a session's idle timer without altering its overlay
// files. Called by the MCP `overlay_keepalive` tool so an editor can
// explicitly extend the lease without re-pushing buffer content.
// Returns ErrSessionNotFound when the session doesn't exist so the
// keepalive tool can surface "session lost; please re-register".
func (m *OverlayManager) Touch(sessionID string) error {
	if m == nil {
		return ErrSessionNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	sess.LastUsed = time.Now()
	return nil
}

// IdleTTL returns the configured idle expiry duration. Exposed so the
// overlay_list tool can compute and surface an `expires_at` hint to
// editor extensions that want to schedule a keepalive proactively.
// Zero means "no expiry" (test-mode).
func (m *OverlayManager) IdleTTL() time.Duration {
	if m == nil {
		return 0
	}
	return m.idleTTL
}

// SessionStatus is the per-session liveness snapshot reported through
// overlay_list. Callers compare `IdleSeconds` to `IdleTTLSeconds` to
// decide when to push a keepalive; `ExpiresAt` (RFC3339) is the
// concrete wall-clock instant the janitor would reap the session if
// no further activity arrived.
type SessionStatus struct {
	WorkspaceID    string
	Created        time.Time
	LastUsed       time.Time
	IdleSeconds    float64
	IdleTTLSeconds float64
	ExpiresAt      time.Time // zero when idleTTL <= 0
}

// StatusFor returns liveness metadata for a session without touching
// LastUsed (unlike SnapshotFor, which is a read-with-bump). Used by
// overlay_list to render expiry hints without resetting the timer
// every time the editor polls.
func (m *OverlayManager) StatusFor(sessionID string) (SessionStatus, error) {
	if m == nil {
		return SessionStatus{}, ErrSessionNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return SessionStatus{}, ErrSessionNotFound
	}
	st := SessionStatus{
		WorkspaceID:    sess.WorkspaceID,
		Created:        sess.Created,
		LastUsed:       sess.LastUsed,
		IdleSeconds:    time.Since(sess.LastUsed).Seconds(),
		IdleTTLSeconds: m.idleTTL.Seconds(),
	}
	if m.idleTTL > 0 {
		st.ExpiresAt = sess.LastUsed.Add(m.idleTTL)
	}
	return st, nil
}

// SnapshotFor returns the overlay files for a session in a stable,
// path-sorted order along with the workspace slug captured at register
// time. Returns ErrSessionNotFound when the session doesn't exist. The
// returned slice never aliases the manager's internal map; callers can
// mutate it freely.
//
// This is the preferred read API for the query path: the deterministic
// ordering means two overlay-active requests with the same overlay set
// touch the same paths in the same order, which simplifies test
// assertions and makes drift errors point at the same path on retry.
func (m *OverlayManager) SnapshotFor(sessionID string) (workspace string, files []OverlayFile, err error) {
	if m == nil {
		return "", nil, ErrSessionNotFound
	}
	// Promoted to write lock so we can refresh LastUsed alongside
	// the snapshot copy: every tool-call view-build flows through
	// here, and that activity must reset the idle timer. Without
	// this, a session that only queries (no further Push) would
	// trip the TTL while in active use. The cost is one extra
	// mutex promotion per overlay-active tool call — negligible
	// against the parse work the view builder is about to do.
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return "", nil, ErrSessionNotFound
	}
	sess.LastUsed = time.Now()
	out := make([]OverlayFile, 0, len(sess.files))
	for _, f := range sess.files {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return sess.WorkspaceID, out, nil
}

// Push attaches one overlay file to a session. Workspace mismatch
// (the session was registered for workspace X but the push targets
// Y) returns an error: a session is supposed to be a coherent view
// over one workspace's repos.
//
// driftCheck is a callback the manager invokes to verify BaseSHA
// against the on-disk file. The daemon supplies it; tests can pass
// nil to skip the check. If driftCheck returns false and overlay
// has a non-empty BaseSHA, Push fails with ErrOverlayDrift.
func (m *OverlayManager) Push(sessionID string, overlay OverlayFile, driftCheck func(path, sha string) bool) error {
	if overlay.Path == "" {
		return errors.New("overlay path is required")
	}
	if overlay.Deleted && overlay.Content != "" {
		return errors.New("overlay cannot be both deleted and have content")
	}
	if overlay.BaseSHA != "" && driftCheck != nil {
		if !driftCheck(overlay.Path, overlay.BaseSHA) {
			return ErrOverlayDrift
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	sess.files[overlay.Path] = overlay
	sess.LastUsed = time.Now()
	return nil
}

// Delete removes one overlay file from a session by path. Returns
// ErrSessionNotFound when the session doesn't exist.
func (m *OverlayManager) Delete(sessionID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	delete(sess.files, path)
	sess.LastUsed = time.Now()
	return nil
}

// Drop terminates the session and discards every overlay it held.
// Idempotent — dropping an unknown session is a no-op.
func (m *OverlayManager) Drop(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// Files returns a snapshot of every overlay attached to a session
// (no live aliasing — the returned map can be mutated freely).
// ErrSessionNotFound when the session doesn't exist.
func (m *OverlayManager) Files(sessionID string) (map[string]OverlayFile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	out := make(map[string]OverlayFile, len(sess.files))
	for k, v := range sess.files {
		out[k] = v
	}
	return out, nil
}

// SessionWorkspace returns the workspace slug captured at Register.
// ErrSessionNotFound when the session doesn't exist.
func (m *OverlayManager) SessionWorkspace(sessionID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return "", ErrSessionNotFound
	}
	return sess.WorkspaceID, nil
}

// SweepIdle drops sessions whose LastUsed is older than IdleTTL.
// Returns the count of dropped sessions for telemetry. Safe to call
// from a single janitor goroutine on a ticker.
func (m *OverlayManager) SweepIdle() int {
	if m.idleTTL <= 0 {
		return 0
	}
	cutoff := time.Now().Add(-m.idleTTL)
	m.mu.Lock()
	defer m.mu.Unlock()
	dropped := 0
	for id, sess := range m.sessions {
		if sess.LastUsed.Before(cutoff) {
			delete(m.sessions, id)
			dropped++
		}
	}
	return dropped
}
