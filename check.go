package fuge

import (
	"bytes"
	"fmt"
	"os"
)

// Check compares freshly generated document bytes against the committed
// artifact at path, so contract-drift gating runs as one tested code path
// locally and in CI instead of shell logic in a workflow.
func Check(generated []byte, path string) error {
	committed, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading committed artifact: %w", err)
	}

	if !bytes.Equal(generated, committed) {
		return fmt.Errorf("%s is stale vs the generator output (%d vs %d bytes) — run 'just api-sync' and commit",
			path, len(committed), len(generated))
	}

	return nil
}
