package fuge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test_Check_Passes_When_File_Matches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, []byte(`{"asyncapi":"3.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Check([]byte(`{"asyncapi":"3.0.0"}`), path); err != nil {
		t.Fatalf("want nil for matching file, got %v", err)
	}
}

func Test_Check_Fails_On_Stale_File_With_Actionable_Message(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	if err := os.WriteFile(path, []byte(`{"asyncapi":"2.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Check([]byte(`{"asyncapi":"3.0.0"}`), path)
	if err == nil {
		t.Fatal("want error for stale file, got nil")
	}

	for _, want := range []string{path, "just api-sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q", err, want)
		}
	}
}

func Test_Check_Fails_On_Missing_File(t *testing.T) {
	if err := Check([]byte(`{}`), filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}
