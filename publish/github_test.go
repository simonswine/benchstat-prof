// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGHAPI(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" > "$GH_TEST_ARGS"
if [ "$GH_TEST_STATUS" = "404" ]; then
 printf 'HTTP/2.0 404 Not Found\r\nContent-Type: application/json\r\n\r\n{"message":"Not Found"}\n'
 exit 1
fi
if [ "$GH_TEST_STATUS" = "auth" ]; then
 echo 'please authenticate using gh auth login' >&2
 exit 1
fi
if [ "$GH_TEST_STATUS" = "204" ]; then
 printf 'HTTP/2.0 204 No Content\r\n\r\n'
 exit 0
fi
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--input" ]; then cat > "$GH_TEST_BODY"; break; fi
 shift
done
printf 'HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n\r\n{"sha":"test-sha"}\n'
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TEST_ARGS", filepath.Join(dir, "args"))
	t.Setenv("GH_TEST_BODY", filepath.Join(dir, "body"))
	t.Setenv("GH_TEST_STATUS", "200")
	var result struct {
		SHA string `json:"sha"`
	}
	if err := (ghAPI{}).call(context.Background(), "POST", "repos/alice/reports/git/blobs", map[string]string{"content": "AAEC/w==", "encoding": "base64"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.SHA != "test-sha" {
		t.Fatal(result)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--hostname\ngithub.com\n--include\n--method\nPOST\n") {
		t.Fatal(string(args))
	}
	body, err := os.ReadFile(filepath.Join(dir, "body"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "AAEC/w==") {
		t.Fatal(string(body))
	}
	t.Setenv("GH_TEST_STATUS", "404")
	if err := (ghAPI{}).call(context.Background(), "GET", "missing", nil, nil); !hasStatus(err, 404) {
		t.Fatal(err)
	}
	t.Setenv("GH_TEST_STATUS", "auth")
	if err := (ghAPI{}).call(context.Background(), "GET", "user", nil, nil); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatal(err)
	}
	t.Setenv("GH_TEST_STATUS", "204")
	if err := (ghAPI{}).call(context.Background(), "PUT", "pages", map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
}
