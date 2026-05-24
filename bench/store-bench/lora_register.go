//go:build lora

package main

import (
	"os"
	"path/filepath"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_lora"
)

func init() {
	loraFactory = func() (graph.Store, func() int64, error) {
		dir, err := os.MkdirTemp("", "store-bench-lora-*")
		if err != nil {
			return nil, nil, err
		}
		path := filepath.Join(dir, "store.lora")
		s, err := store_lora.Open(path)
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, err
		}
		diskFn := func() int64 {
			_ = s.Close()
			return dirSize(dir)
		}
		return s, diskFn, nil
	}
}
