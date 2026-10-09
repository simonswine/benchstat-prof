// SPDX-License-Identifier: Apache-2.0
package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveTarget(context.Background(), dir, "path:"+dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.dir != dir || got.sha != "" {
		t.Fatalf("unexpected directory target: %+v", got)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", "path:", "path:" + file, "path:" + filepath.Join(dir, "missing")} {
		if _, err := resolveTarget(context.Background(), dir, ref); err == nil {
			t.Errorf("invalid target accepted: %q", ref)
		}
	}
}
