package telemetry

import "strings"

// This file holds the small, named record-site helpers the rest of the codebase
// calls so the event taxonomy lives in one place. Each is nil-safe (a nil
// recorder is a no-op) and routes through Recorder.Record, so the consent gate,
// the hard allow-list, and the dimension sanitiser still apply — a record site
// can never widen what leaves the process.

// RecordIndex records one completed index pass: a coarse file-count bucket plus
// a per-language counter for each language present. Exact counts and paths
// never leave the process — only the bucket label and bounded language tokens.
// Beyond the bucket codegraph would record, the per-language breakdown rides
// the same hard allow-list + sanitiser, so it adds signal without widening the
// privacy surface.
func RecordIndex(rec *Recorder, fileCount int, langs []string) {
	if rec == nil {
		return
	}
	rec.Record("index", BucketFileCount(fileCount))
	seen := make(map[string]bool, len(langs))
	for _, lang := range langs {
		lang = strings.TrimSpace(lang)
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		rec.Record("index_lang", lang)
	}
}

// RecordDaemonSession records one daemon session start, dimensioned by the
// backend kind (memory / sqlite). One event per daemon process.
func RecordDaemonSession(rec *Recorder, backend string) {
	if rec == nil {
		return
	}
	rec.Record("daemon_session", backend)
}

// RecordInstall records one install / uninstall, dimensioned by the agent
// target or scope (e.g. "claude", "global") — never a path or user identifier.
// action selects the allow-listed key: "install" (default) or "uninstall".
func RecordInstall(rec *Recorder, action, target string) {
	if rec == nil {
		return
	}
	key := "install"
	if action == "uninstall" {
		key = "uninstall"
	}
	rec.Record(key, target)
}

// RecordClient folds an MCP client application name into the rollup. The name
// is normalised to its first whitespace-delimited token, lowercased, so a
// "claude-code 1.0.42" handshake records the bounded token "claude-code" and
// the version (an identifying axis) is dropped before it reaches the sanitiser.
func RecordClient(rec *Recorder, clientName string) {
	if rec == nil {
		return
	}
	dim := NormalizeClientName(clientName)
	if dim == "" {
		return
	}
	rec.Record("client", dim)
}

// NormalizeClientName reduces an MCP clientInfo.name to a bounded, version-free
// token: the first whitespace-delimited field, lowercased. Returns "" for an
// empty / whitespace-only name. Exported so the snapshot path can pre-validate.
func NormalizeClientName(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}
