//go:build cozo

package main

import (
	"os"
	"path/filepath"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cozo"
)

func init() {
	cozoFactory = func() (graph.Store, func() int64, error) {
		dir, err := os.MkdirTemp("", "store-bench-cozo-*")
		if err != nil {
			return nil, nil, err
		}
		path := filepath.Join(dir, "store.cozo")
		s, err := store_cozo.Open(path)
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, err
		}
		diskFn := func() int64 {
			_ = s.Close()
			return dirSize(path)
		}
		return s, diskFn, nil
	}
}
