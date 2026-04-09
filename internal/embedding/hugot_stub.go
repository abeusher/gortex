//go:build !embeddings_hugot

package embedding

import "errors"

func newHugotProvider() (Provider, error) {
	return nil, errors.New("hugot provider not compiled in (build with -tags embeddings_hugot)")
}
