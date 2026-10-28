package fuge

import (
	"encoding/json"
	"testing"
)

func Test_MergeSchemas_Combines_And_Accepts_Identical_Duplicates(t *testing.T) {
	dst := map[string]json.RawMessage{
		"A": json.RawMessage(`{"type":"string"}`),
	}
	src := map[string]json.RawMessage{
		"A": json.RawMessage(`{"type":"string"}`),
		"B": json.RawMessage(`{"type":"integer"}`),
	}

	if err := MergeSchemas(dst, src); err != nil {
		t.Fatal(err)
	}

	if len(dst) != 2 {
		t.Fatalf("want 2 schemas, got %d", len(dst))
	}

	if string(dst["B"]) != `{"type":"integer"}` {
		t.Fatalf("B not merged: %s", dst["B"])
	}
}

func Test_MergeSchemas_Rejects_Conflicting_Definitions(t *testing.T) {
	dst := map[string]json.RawMessage{
		"A": json.RawMessage(`{"type":"string"}`),
	}
	src := map[string]json.RawMessage{
		"A": json.RawMessage(`{"type":"integer"}`),
	}

	if err := MergeSchemas(dst, src); err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}
