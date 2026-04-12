package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

// Session represents one proxy or CLI connection to the daemon. Per-session
// state (recent activity, symbol history, token stats for this client)
// lives here; shared state (the graph, feedback store, cumulative savings)
// lives on the Server.
//
// A Session is created on a successful handshake and destroyed when its
// socket connection closes. The daemon routes every inbound frame to its
// session by looking up the net.Conn in the session registry.
type Session struct {
	ID            string
	Mode          ConnectionMode
	CWD           string
	ClientName    string
	ClientPID     int
	DefaultRepo   string
	ActiveProject string
	StartedAt     time.Time

	// Conn is the underlying socket. Kept for close-on-shutdown and
	// logging; handlers should not read from or write to it directly —
	// framing is the transport's job.
	Conn net.Conn

	// Per-session mutable state that will move over from internal/mcp's
	// Server during the session-isolation refactor. Left as interface{}
	// for now so the types can evolve without churning this file every
	// iteration — the refactor will replace this with concrete pointers.
	SessionState any
	SymHistory   any
	TokenStats   any
}

// SessionRegistry tracks active sessions. Safe for concurrent access from
// the accept goroutine and the control-surface handlers.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*Session // session_id → Session
	byConn   map[net.Conn]*Session
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[string]*Session),
		byConn:   make(map[net.Conn]*Session),
	}
}

// Register creates and stores a new session for the given connection.
// Called after a successful handshake. Generates the session ID.
func (r *SessionRegistry) Register(conn net.Conn, h Handshake) *Session {
	s := &Session{
		ID:         newSessionID(),
		Mode:       h.Mode,
		CWD:        h.CWD,
		ClientName: h.ClientName,
		ClientPID:  h.PID,
		StartedAt:  time.Now(),
		Conn:       conn,
	}
	r.mu.Lock()
	r.sessions[s.ID] = s
	r.byConn[conn] = s
	r.mu.Unlock()
	return s
}

// Remove deletes the session for a connection. Idempotent — safe to call
// from both the accept-loop's defer and the shutdown path.
func (r *SessionRegistry) Remove(conn net.Conn) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.byConn[conn]
	if s == nil {
		return nil
	}
	delete(r.byConn, conn)
	delete(r.sessions, s.ID)
	return s
}

// Get returns the session for a connection, or nil if the connection hasn't
// completed its handshake yet (or was already removed).
func (r *SessionRegistry) Get(conn net.Conn) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byConn[conn]
}

// Count returns the number of live sessions — used by the status command
// and for metrics.
func (r *SessionRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// All returns a snapshot of every live session. The caller must not
// mutate the returned Session objects; they're shared with the registry.
func (r *SessionRegistry) All() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// newSessionID generates a short URL-safe identifier. 8 bytes of entropy
// gives us 16 hex chars — collision-resistant enough for a per-user
// single-process registry without bloating log lines.
func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sess_" + hex.EncodeToString(b[:])
}
