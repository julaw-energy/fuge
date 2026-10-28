package fuge

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MergeSchemas folds src into dst, failing on same-name definitions with
// different content — a silent overwrite would corrupt one of the schemas.
func MergeSchemas(dst, src map[string]json.RawMessage) error {
	for name, schema := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = schema

			continue
		}

		if !bytes.Equal(existing, schema) {
			return fmt.Errorf("conflicting schema definitions for %q", name)
		}
	}

	return nil
}
