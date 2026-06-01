package indexer

import (
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgtdi/fswatcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/search"
)

// toggleExtractor is a parser.Extractor whose Extract result is flipped
// at runtime. In good mode it emits a file node plus one function node;
// in fail mode it returns (nil, err) — the exact shape that drives
// indexFile's `result == nil` branch (a transient parse failure /
// quarantine), which tree-sitter's error-tolerant grammars never
// produce for real Go source. The custom ".fk" extension keeps it off
// every other extractor's turf.
type toggleExtractor struct {
	mu    sync.Mutex
	fail  bool
	funcs []string
}

func (e *toggleExtractor) Language() string     { return "faketoggle" }
func (e *toggleExtractor) Extensions() []string { return []string{".fk"} }

func (e *toggleExtractor) setFail(f bool) {
	e.mu.Lock()
	e.fail = f
	e.mu.Unlock()
}

func (e *toggleExtractor) setFuncs(names ...string) {
	e.mu.Lock()
	e.funcs = names
	e.mu.Unlock()
}

func (e *toggleExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	e.mu.Lock()
	fail := e.fail
	funcs := append([]string(nil), e.funcs...)
	e.mu.Unlock()
	if fail {
		return nil, errors.New("toggleExtractor: forced parse failure")
	}
	nodes := []*graph.Node{{
		ID:       filePath,
		Kind:     graph.KindFile,
		Name:     filepath.Base(filePath),
		FilePath: filePath,
		Language: "faketoggle",
	}}
	for _, fn := range funcs {
		nodes = append(nodes, &graph.Node{
			ID:       filePath + "::" + fn,
			Kind:     graph.KindFunction,
			Name:     fn,
			FilePath: filePath,
			Language: "faketoggle",
		})
	}
	return &parser.ExtractionResult{Nodes: nodes}, nil
}

func newToggleIndexer(t *testing.T) (*Indexer, *toggleExtractor) {
	t.Helper()
	ext := &toggleExtractor{}
	reg := parser.NewRegistry()
	reg.Register(ext)
	g := graph.New()
	idx := New(g, reg, config.IndexConfig{Workers: 1}, zap.NewNop())
	idx.search = search.NewBM25()
	return idx, ext
}

func searchHasID(idx *Indexer, query, id string) bool {
	for _, r := range idx.search.Search(query, 50) {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestIndexFile_ParseFailureKeepsPriorNodes is the central proof of the
// parse-then-swap fix: re-indexing a file whose new bytes are
// unparseable must NOT zero the file's prior nodes / edges / search
// entries. Stale-but-present beats empty. A clean re-index then swaps
// them as normal.
//
// Against the pre-fix evict-first code this test FAILS: indexFile
// evicted the graph + search entries before parsing and returned early
// on result == nil, leaving the file at zero nodes.
func TestIndexFile_ParseFailureKeepsPriorNodes(t *testing.T) {
	idx, ext := newToggleIndexer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fk")
	idx.SetRootPath(dir)

	// First index, good mode — one function lands in graph + search.
	ext.setFail(false)
	ext.setFuncs("Alpha")
	writeFile(t, path, "alpha body")
	require.NoError(t, idx.IndexFile(path))

	funcID := "main.fk::Alpha"
	require.NotNil(t, idx.graph.GetNode(funcID), "Alpha must be indexed before the bad edit")
	require.True(t, searchHasID(idx, "Alpha", funcID), "Alpha must be in the search index before the bad edit")
	nodesBefore := len(idx.graph.GetFileNodes("main.fk"))
	require.Equal(t, 2, nodesBefore, "file node + Alpha")

	// Save a transiently unparseable edit. extractFile returns
	// (nil, err); indexFile must NOT evict.
	ext.setFail(true)
	writeFile(t, path, "this no longer parses")
	require.Error(t, idx.IndexFile(path),
		"a failed parse should surface the extractor error")

	// The prior state survives, untouched.
	assert.Equal(t, nodesBefore, len(idx.graph.GetFileNodes("main.fk")),
		"a failed re-index must leave the file's prior nodes intact, not zero them")
	assert.NotNil(t, idx.graph.GetNode(funcID),
		"Alpha must still exist after the failed re-index")
	assert.True(t, searchHasID(idx, "Alpha", funcID),
		"Alpha must still be in the search index after the failed re-index")

	// A subsequent valid re-index swaps cleanly: Alpha gone, Beta in.
	ext.setFail(false)
	ext.setFuncs("Beta")
	writeFile(t, path, "beta body")
	require.NoError(t, idx.IndexFile(path))

	assert.Nil(t, idx.graph.GetNode(funcID),
		"a successful re-index must evict the old Alpha node")
	assert.False(t, searchHasID(idx, "Alpha", funcID),
		"a successful re-index must remove Alpha's stale search entry")
	betaID := "main.fk::Beta"
	assert.NotNil(t, idx.graph.GetNode(betaID), "Beta must be indexed by the clean swap")
	assert.True(t, searchHasID(idx, "Beta", betaID), "Beta must be in the search index after the clean swap")
}

// TestPatchGraphModify_ParseFailureKeepsPriorNodes proves the LIVE
// watcher path is parse-safe, not just indexFile in isolation. The
// editor-save path goes Watcher event -> patchGraph(ChangeModified),
// which used to call EvictFile BEFORE IndexFile — so a transiently
// unparseable save dropped the file's nodes even with indexFile itself
// fixed. With the pre-evict removed, a failed modify through patchGraph
// must leave the file's prior nodes / search entries intact, and a clean
// modify must still swap. Against the pre-fix patchGraph this FAILS.
func TestPatchGraphModify_ParseFailureKeepsPriorNodes(t *testing.T) {
	idx, ext := newToggleIndexer(t)
	dir := t.TempDir()
	idx.SetRootPath(dir)
	path := filepath.Join(dir, "main.fk")

	w, err := NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)

	// Initial index through the live create patch.
	ext.setFail(false)
	ext.setFuncs("Alpha")
	writeFile(t, path, "alpha body")
	w.patchGraph(path, ChangeCreated)

	funcID := "main.fk::Alpha"
	require.NotNil(t, idx.graph.GetNode(funcID), "Alpha must be indexed via the create patch")
	require.True(t, searchHasID(idx, "Alpha", funcID))
	nodesBefore := len(idx.graph.GetFileNodes("main.fk"))
	require.Equal(t, 2, nodesBefore, "file node + Alpha")

	// A transiently-unparseable save arrives as a Modify on the live path.
	ext.setFail(true)
	writeFile(t, path, "this no longer parses")
	w.patchGraph(path, ChangeModified)

	assert.Equal(t, nodesBefore, len(idx.graph.GetFileNodes("main.fk")),
		"a failed modify through the live watcher path must not zero the file's nodes")
	assert.NotNil(t, idx.graph.GetNode(funcID), "Alpha must survive the failed live modify")
	assert.True(t, searchHasID(idx, "Alpha", funcID), "Alpha's search entry must survive the failed live modify")

	// A clean modify swaps cleanly.
	ext.setFail(false)
	ext.setFuncs("Beta")
	writeFile(t, path, "beta body")
	w.patchGraph(path, ChangeModified)
	assert.Nil(t, idx.graph.GetNode(funcID), "a clean live modify evicts Alpha")
	assert.NotNil(t, idx.graph.GetNode("main.fk::Beta"), "a clean live modify indexes Beta")
}

// TestPollGitHead_DiffFailureRetriesRange proves the lastSHA-advance
// fix: when the git diff for the moved range errors, pollGitHead must
// leave lastSHA at the old SHA so the next cycle retries the same
// (un-reconciled) range. Advancing it on failure would permanently skip
// that span.
//
// We force the diff failure by seeding lastSHA with a bogus SHA — `git
// diff <bogus>..HEAD` errors with "unknown revision". The fix then
// requires lastSHA to stay bogus across the failing poll, and the range
// to reconcile once lastSHA is a real prior commit.
func TestPollGitHead_DiffFailureRetriesRange(t *testing.T) {
	if !haveGit(t) {
		t.Skip("git binary not available in PATH")
	}
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-q", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	runGit(t, repoDir, "config", "commit.gpgsign", "false")

	writeFile(t, filepath.Join(repoDir, "a.go"), "package main\nfunc Alpha() {}\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-q", "-m", "main: Alpha")
	firstSHA, err := pollerHeadSHA(repoDir)
	require.NoError(t, err)

	writeFile(t, filepath.Join(repoDir, "b.go"), "package main\nfunc Beta() {}\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-q", "-m", "main: Beta")

	g := graph.New()
	idx := New(g, newTestRegistry(), config.IndexConfig{Workers: 1}, zap.NewNop())
	idx.search = search.NewBM25()
	idx.SetRootPath(repoDir)
	_, err = idx.IndexCtx(testCtx(), repoDir)
	require.NoError(t, err)
	require.NotEmpty(t, g.GetFileNodes("a.go"))
	require.NotEmpty(t, g.GetFileNodes("b.go"))

	w, err := NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)
	p := newPoller(w, idx, zap.NewNop())

	// Seed lastSHA with a SHA git can't resolve — the diff for this
	// cycle's range will error.
	const bogus = "0000000000000000000000000000000000000000"
	p.mu.Lock()
	p.lastSHA = bogus
	p.mu.Unlock()

	// Failing cycle: diff errors, so lastSHA must NOT advance.
	require.False(t, p.pollGitHead(), "a failed diff reports no reconcile")
	p.mu.Lock()
	stuck := p.lastSHA
	p.mu.Unlock()
	require.Equal(t, bogus, stuck,
		"a failed git diff must leave lastSHA at the old SHA so the range is retried, not skipped")

	// Now point lastSHA at a real prior commit; the retry reconciles
	// the same HEAD range that the failure left un-reconciled.
	p.mu.Lock()
	p.lastSHA = firstSHA
	p.mu.Unlock()
	require.True(t, p.pollGitHead(), "the retry must reconcile the previously-failed range")
	head, err := pollerHeadSHA(repoDir)
	require.NoError(t, err)
	p.mu.Lock()
	settled := p.lastSHA
	p.mu.Unlock()
	assert.Equal(t, head, settled, "a successful diff advances lastSHA to HEAD")
}

func haveGit(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("git")
	return err == nil
}

// TestWatcher_OverflowEventTriggersReconcile proves the kernel-overflow
// gap is closed: a pathless EventOverflow on the Events channel triggers
// a coalesced full-tree reconcile (the signal the Linux inotify backend
// raises when its queue overflows and events are lost). The reconcileFn
// seam stands in for IncrementalReindex so the assertion is
// deterministic and platform-independent.
func TestWatcher_OverflowEventTriggersReconcile(t *testing.T) {
	idx, _ := newToggleIndexer(t)
	idx.SetRootPath(t.TempDir())
	w, err := NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)

	var calls int32
	done := make(chan struct{}, 1)
	w.reconcileMu.Lock()
	w.reconcileFn = func() {
		atomic.AddInt32(&calls, 1)
		select {
		case done <- struct{}{}:
		default:
		}
	}
	w.reconcileMu.Unlock()

	w.handleEvent(fswatcher.WatchEvent{Types: []fswatcher.EventType{fswatcher.EventOverflow}})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow event did not trigger a reconcile")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "exactly one reconcile from one overflow")
}

// TestWatcher_OverflowReconcileCoalesces proves a burst of overflow
// signals collapses into at most one reconcile in flight — the loop is
// never blocked and the tree isn't re-walked per dropped event.
func TestWatcher_OverflowReconcileCoalesces(t *testing.T) {
	idx, _ := newToggleIndexer(t)
	idx.SetRootPath(t.TempDir())
	w, err := NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)

	var calls int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	w.reconcileMu.Lock()
	w.reconcileFn = func() {
		atomic.AddInt32(&calls, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // hold the reconcile "in flight"
	}
	w.reconcileMu.Unlock()

	// First signal starts the (blocked) reconcile.
	w.triggerOverflowReconcile("queue-overflow")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first reconcile never started")
	}

	// A burst while one is in flight must be coalesced away.
	for i := 0; i < 25; i++ {
		w.triggerOverflowReconcile("queue-overflow")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls),
		"overflow signals during an in-flight reconcile must coalesce to one")

	close(release)
	// Once the in-flight reconcile drains, a fresh signal runs again.
	require.Eventually(t, func() bool {
		w.reconcileMu.Lock()
		pending := w.reconcilePending
		w.reconcileMu.Unlock()
		return !pending
	}, 2*time.Second, 5*time.Millisecond, "reconcilePending must clear after the reconcile finishes")
}

// TestWatcher_OverflowReconcileIndexesMissedFile is the end-to-end proof
// that the real reconcile path (IncrementalReindex) recovers a file
// whose create/modify event was lost. We index a tree, drop a brand-new
// file on disk (simulating a missed inotify create), then drive an
// overflow through the real reconcile and assert the new file is now in
// the graph.
func TestWatcher_OverflowReconcileIndexesMissedFile(t *testing.T) {
	dir := t.TempDir()
	ext := &toggleExtractor{}
	reg := parser.NewRegistry()
	reg.Register(ext)
	g := graph.New()
	idx := New(g, reg, config.IndexConfig{Workers: 1}, zap.NewNop())
	idx.search = search.NewBM25()
	idx.SetRootPath(dir)

	ext.setFail(false)
	ext.setFuncs("Seed")
	writeFile(t, filepath.Join(dir, "seed.fk"), "seed body")
	_, err := idx.IndexCtx(testCtx(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, g.GetFileNodes("seed.fk"))

	w, err := NewWatcher(idx, config.WatchConfig{Enabled: true, DebounceMs: 10}, zap.NewNop())
	require.NoError(t, err)

	// A new file appears on disk but its create event was "lost".
	ext.setFuncs("Recovered")
	writeFile(t, filepath.Join(dir, "missed.fk"), "recovered body")
	require.Empty(t, g.GetFileNodes("missed.fk"), "missed file must be absent before the reconcile")

	// Drive the real IncrementalReindex through the overflow path, with
	// a thin wrapper only to know when it finishes.
	done := make(chan struct{}, 1)
	w.reconcileMu.Lock()
	w.reconcileFn = func() {
		_, rerr := idx.IncrementalReindex(dir)
		require.NoError(t, rerr)
		done <- struct{}{}
	}
	w.reconcileMu.Unlock()

	w.handleEvent(fswatcher.WatchEvent{Types: []fswatcher.EventType{fswatcher.EventOverflow}})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("overflow reconcile never ran")
	}

	assert.NotEmpty(t, g.GetFileNodes("missed.fk"),
		"the overflow-driven reconcile must index the previously-missed file")
}
