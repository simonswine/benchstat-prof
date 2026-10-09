// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simonswine/benchstat-prof/evidence"
	"go.yaml.in/yaml/v3"
)

type fakeAPI struct {
	exists, pages, conflict, duplicateOnConflict bool
	head                                         string
	files, blobs                                 map[string]string
	pending                                      map[string]string
	calls                                        []string
	pagesInput                                   map[string]any
	creates                                      int
	readme                                       string
}

func newFake() *fakeAPI {
	return &fakeAPI{exists: true, head: "head", files: map[string]string{}, blobs: map[string]string{}}
}
func (f *fakeAPI) call(_ context.Context, method, endpoint string, input, output any) error {
	f.calls = append(f.calls, method+" "+endpoint)
	var in map[string]any
	if input != nil {
		data, _ := json.Marshal(input)
		_ = json.Unmarshal(data, &in)
	}
	var result any = map[string]any{}
	root := "repos/alice/benchstat-prof-reports"
	switch {
	case endpoint == "user":
		result = map[string]string{"login": "alice"}
	case endpoint == "user/repos":
		f.exists = true
		f.creates++
	case endpoint == root:
		if !f.exists {
			return &apiError{404, "missing repo"}
		}
		result = map[string]any{"size": 1, "default_branch": "main"}
	case endpoint == root+"/contents/README.md?ref=main":
		if f.readme == "" {
			return &apiError{404, "missing README"}
		}
		result = map[string]string{"type": "file", "encoding": "base64", "sha": "readme-sha", "content": base64.StdEncoding.EncodeToString([]byte(f.readme))}
	case endpoint == root+"/contents/README.md":
		if in["branch"] != "main" {
			return fmt.Errorf("README not on default branch")
		}
		data, err := base64.StdEncoding.DecodeString(in["content"].(string))
		if err != nil {
			return err
		}
		f.readme = string(data)
	case endpoint == root+"/git/ref/heads/pages":
		if f.head == "" {
			return &apiError{404, "missing branch"}
		}
		result = map[string]any{"object": map[string]string{"sha": f.head}}
	case strings.HasPrefix(endpoint, root+"/git/commits/"):
		result = map[string]any{"tree": map[string]string{"sha": "tree"}}
	case strings.HasPrefix(endpoint, root+"/git/trees/"):
		var tree []map[string]string
		for path, sha := range f.files {
			tree = append(tree, map[string]string{"path": path, "sha": sha, "type": "blob", "mode": "100644"})
		}
		result = map[string]any{"tree": tree}
	case strings.HasPrefix(endpoint, root+"/git/blobs/"):
		sha := strings.TrimPrefix(endpoint, root+"/git/blobs/")
		result = map[string]string{"encoding": "base64", "content": f.blobs[sha]}
	case endpoint == root+"/git/blobs":
		sha := fmt.Sprintf("blob%d", len(f.blobs))
		f.blobs[sha] = in["content"].(string)
		result = map[string]string{"sha": sha}
	case endpoint == root+"/git/trees":
		f.pending = map[string]string{}
		if in["base_tree"] != nil {
			for p, s := range f.files {
				f.pending[p] = s
			}
		}
		for _, v := range in["tree"].([]any) {
			item := v.(map[string]any)
			f.pending[item["path"].(string)] = item["sha"].(string)
		}
		result = map[string]string{"sha": "new-tree"}
	case endpoint == root+"/git/commits":
		result = map[string]string{"sha": "new-commit"}
	case endpoint == root+"/git/refs/heads/pages" || endpoint == root+"/git/refs":
		if in["force"] == true {
			return fmt.Errorf("force update forbidden")
		}
		if f.conflict {
			f.conflict = false
			f.head = "concurrent-head"
			if f.duplicateOnConflict {
				data, _ := yaml.Marshal(catalogue{Version: 1, Reports: []entry{{ID: "run", Path: "reports/run/report.html"}}})
				f.blobs["concurrent-index"] = base64.StdEncoding.EncodeToString(data)
				f.files["index.yaml"] = "concurrent-index"
			} else {
				f.files["unrelated.txt"] = "existing-blob"
			}
			return &apiError{422, "not a fast forward"}
		}
		f.files = f.pending
		f.head = "new-commit"
	case endpoint == root+"/pages":
		if method == "GET" {
			if !f.pages {
				return &apiError{404, "missing Pages"}
			}
			result = map[string]string{"html_url": "https://example.test/custom/"}
		} else {
			f.pages = true
			f.pagesInput = in
		}
	default:
		return fmt.Errorf("unexpected API call: %s %s", method, endpoint)
	}
	if output != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, output)
	}
	return nil
}
func fixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	for path, data := range map[string][]byte{
		"report.html": []byte("<html>web report</html>"), "report-local.html": []byte("<html>local</html>"),
		"plan.yaml":                []byte("baseline:\n  ref: main\ncomparison:\n  ref: feature\n"),
		"status.yaml":              []byte("started_at: 2026-10-09T15:00:00Z\n"),
		"baseline/bench/cpu.pprof": {0, 1, 2, 255}, "comparison/bench/heap.pprof": {5, 6, 7},
		"baseline/bench/test.bin": []byte("do not publish"), "run.log": []byte("private log"),
	} {
		name := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func decoded(t *testing.T, f *fakeAPI, path string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(f.blobs[f.files[path]])
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestPublish(t *testing.T) {
	dir := fixture(t)
	f := newFake()
	f.files["old.txt"] = "old-blob"
	link, err := run(context.Background(), f, dir, Options{}, time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://example.test/custom/reports/run/report.html" {
		t.Fatal(link)
	}
	if f.files["old.txt"] != "old-blob" {
		t.Fatal("lost existing files")
	}
	if !bytes.Equal(decoded(t, f, "reports/run/baseline/bench/cpu.pprof"), []byte{0, 1, 2, 255}) {
		t.Fatal("profile changed")
	}
	for path := range f.files {
		if strings.HasSuffix(path, "test.bin") || strings.HasSuffix(path, "run.log") {
			t.Fatal("uploaded private evidence", path)
		}
	}
	var index catalogue
	if err := yaml.Unmarshal(decoded(t, f, "index.yaml"), &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Reports) != 1 || index.Reports[0].Baseline.Ref != "main" {
		t.Fatalf("bad catalogue: %+v", index)
	}
	if !strings.Contains(string(decoded(t, f, "index.html")), "reports/run/report.html") {
		t.Fatal("missing report link")
	}
	if f.pagesInput["build_type"] != "legacy" || f.pagesInput["source"].(map[string]any)["branch"] != "pages" {
		t.Fatal("not branch-based Pages")
	}
	if !strings.Contains(f.readme, "https://example.test/custom/") || !strings.Contains(f.readme, "https://github.com/simonswine/benchstat-prof") {
		t.Fatal("missing README links", f.readme)
	}
	before := len(f.blobs)
	_, err = run(context.Background(), f, dir, Options{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	if len(f.blobs) != before {
		t.Fatal("duplicate uploaded blobs")
	}
}
func TestMissingRepository(t *testing.T) {
	dir := fixture(t)
	f := newFake()
	f.exists = false
	_, err := run(context.Background(), f, dir, Options{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "publish --init --owner alice --repo benchstat-prof-reports") {
		t.Fatal(err)
	}
	if f.creates != 0 {
		t.Fatal("created repository without --init")
	}
	if _, err := run(context.Background(), f, dir, Options{Init: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 {
		t.Fatal("repository not created")
	}
}
func TestConcurrentPublish(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			f := newFake()
			f.conflict = true
			f.duplicateOnConflict = duplicate
			_, err := run(context.Background(), f, fixture(t), Options{}, time.Now())
			if duplicate {
				if err == nil || !strings.Contains(err.Error(), "already exists") {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.files["unrelated.txt"] != "existing-blob" {
				t.Fatal("lost concurrent report")
			}
		})
	}
}
func TestNewPagesBranch(t *testing.T) {
	f := newFake()
	f.head = ""
	f.files["project.txt"] = "project-blob"
	if _, err := run(context.Background(), f, fixture(t), Options{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.files["project.txt"]; ok {
		t.Fatal("copied default branch into site")
	}
}
func TestInvalidCatalogue(t *testing.T) {
	for _, data := range []string{"reports: []", "version: 2\nreports: []", "version: 1\nunknown: true", "version: 1\nreports: []\n---\nversion: 1", "version: 1\nreports:\n- id: ../unsafe\n  path: reports/../unsafe/report.html"} {
		f := newFake()
		f.files["index.yaml"] = "bad"
		f.blobs["bad"] = base64.StdEncoding.EncodeToString([]byte(data))
		if _, err := run(context.Background(), f, fixture(t), Options{}, time.Now()); err == nil {
			t.Fatalf("accepted %q", data)
		}
		if len(f.blobs) != 1 {
			t.Fatal("uploaded before validation")
		}
	}
}
func TestPrepare(t *testing.T) {
	dir := fixture(t)
	if err := os.Remove(filepath.Join(dir, "report.html")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepare(dir, time.Now()); err == nil {
		t.Fatal("accepted missing report")
	}
	dir = fixture(t)
	if err := os.Symlink("report.html", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepare(dir, time.Now()); err == nil {
		t.Fatal("accepted symlink")
	}
}
func TestIndexEscapesMetadata(t *testing.T) {
	var out bytes.Buffer
	if err := indexHTML.Execute(&out, catalogue{Reports: []entry{{ID: "run", Path: "reports/run/report.html", Baseline: evidence.Revision{Ref: "<script>alert(1)</script>"}}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>") {
		t.Fatal("unescaped metadata")
	}
}
