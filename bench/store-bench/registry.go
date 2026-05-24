package main

import "github.com/zzet/gortex/internal/graph"

// cozoFactory / loraFactory are populated by tag-gated init files
// (cozo_register.go, lora_register.go). When the corresponding build
// tag is absent, the factory stays nil and the bench loop skips that
// backend. Cozo and Lora can't ship in the same binary because both
// bundle Rust's libstd and the static archives collide on
// _rust_eh_personality at link time — so they're build-tag-isolated.
var (
	cozoFactory func() (graph.Store, func() int64, error)
	loraFactory func() (graph.Store, func() int64, error)
)
