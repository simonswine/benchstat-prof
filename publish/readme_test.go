// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"context"
	"strings"
	"testing"
)

func TestReadmePreservesExistingContent(t *testing.T) {
	f := newFake()
	f.readme = "# My project\n\nExisting documentation.\n"
	root := "repos/alice/benchstat-prof-reports"
	if err := ensureReadme(context.Background(), f, root, "https://alice.github.io/reports/"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.readme, "# My project\n\nExisting documentation.\n") {
		t.Fatal("lost existing README", f.readme)
	}
	before := f.readme
	if err := ensureReadme(context.Background(), f, root, "https://alice.github.io/reports/"); err != nil {
		t.Fatal(err)
	}
	if f.readme != before {
		t.Fatal("README update not idempotent")
	}
	if err := ensureReadme(context.Background(), f, root, "https://custom.example/"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.readme, "alice.github.io") || strings.Count(f.readme, readmeStart) != 1 {
		t.Fatal("managed section not updated", f.readme)
	}
}
func TestReadmeInvalidMarkers(t *testing.T) {
	if _, err := updateReadme("existing\n"+readmeStart, "section"); err == nil {
		t.Fatal("accepted broken markers")
	}
}
