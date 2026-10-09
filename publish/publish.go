// SPDX-License-Identifier: Apache-2.0
// Package publish publishes run reports using the GitHub CLI's REST API client.
package publish

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simonswine/benchstat-prof/evidence"
	"go.yaml.in/yaml/v3"
)

const DefaultRepo = "benchstat-prof-reports"

// Options selects the repository and explicitly authorizes its creation.
type Options struct {
	Owner string
	Repo  string
	Init  bool
}

type entry struct {
	ID          string            `yaml:"id"`
	Path        string            `yaml:"path"`
	PublishedAt time.Time         `yaml:"published_at"`
	StartedAt   time.Time         `yaml:"started_at,omitempty"`
	Baseline    evidence.Revision `yaml:"baseline"`
	Comparison  evidence.Revision `yaml:"comparison"`
}
type catalogue struct {
	Version int     `yaml:"version"`
	Reports []entry `yaml:"reports"`
}
type artifact struct {
	path string
	data []byte
}
type snapshot struct {
	head, tree string
	files      map[string]string
}

// Run publishes a run directory and returns the report URL. A successful commit
// is not rolled back if Pages configuration subsequently fails.
func Run(ctx context.Context, dir string, opts Options) (string, error) {
	return run(ctx, ghAPI{}, dir, opts, time.Now().UTC())
}

func run(ctx context.Context, client api, dir string, opts Options, now time.Time) (string, error) {
	files, report, err := prepare(dir, now)
	if err != nil {
		return "", err
	}
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	var user struct {
		Login string `json:"login"`
	}
	if opts.Owner == "" || opts.Init {
		if err := client.call(ctx, "GET", "user", nil, &user); err != nil {
			return "", err
		}
		if opts.Owner == "" {
			opts.Owner = user.Login
		}
	}
	if !validComponent(opts.Owner) || !validComponent(opts.Repo) {
		return "", errors.New("owner and repo must be single GitHub name components")
	}
	root := "repos/" + opts.Owner + "/" + opts.Repo
	if err := client.call(ctx, "GET", root, nil, nil); err != nil {
		if !hasStatus(err, 404) {
			return "", err
		}
		if !opts.Init {
			return "", fmt.Errorf("repository %s/%s does not exist; run again with --init to create a public reports repository:\n  benchstat-prof publish --init --owner %s --repo %s %q", opts.Owner, opts.Repo, opts.Owner, opts.Repo, dir)
		}
		endpoint := "user/repos"
		if !strings.EqualFold(opts.Owner, user.Login) {
			var owner struct {
				Type string `json:"type"`
			}
			if err := client.call(ctx, "GET", "users/"+opts.Owner, nil, &owner); err != nil {
				return "", err
			}
			if owner.Type != "Organization" {
				return "", errors.New("cannot create a repository for another user")
			}
			endpoint = "orgs/" + opts.Owner + "/repos"
		}
		if err := client.call(ctx, "POST", endpoint, map[string]any{"name": opts.Repo, "private": false, "auto_init": true, "description": "Published benchstat-prof reports"}, nil); err != nil {
			return "", err
		}
	}
	// Upload immutable blobs once; only catalogue and tree must be regenerated on
	// a concurrent update. Unreferenced blobs on failure are harmless.
	blobs := make(map[string]string)
	for attempt := 0; attempt < 3; attempt++ {
		snap, err := loadSnapshot(ctx, client, root)
		if err != nil {
			return "", err
		}
		index, err := readCatalogue(ctx, client, root, snap)
		if err != nil {
			return "", err
		}
		for _, existing := range index.Reports {
			if existing.ID == report.ID {
				return "", fmt.Errorf("report %q already exists in index.yaml", report.ID)
			}
		}
		prefix := "reports/" + report.ID + "/"
		for path := range snap.files {
			if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
				return "", fmt.Errorf("report destination %q already exists", prefix)
			}
		}
		index.Reports = append(index.Reports, report)
		sort.Slice(index.Reports, func(i, j int) bool {
			if index.Reports[i].PublishedAt.Equal(index.Reports[j].PublishedAt) {
				return index.Reports[i].ID < index.Reports[j].ID
			}
			return index.Reports[i].PublishedAt.After(index.Reports[j].PublishedAt)
		})
		yamlData, err := yaml.Marshal(index)
		if err != nil {
			return "", err
		}
		var html bytes.Buffer
		if err := indexHTML.Execute(&html, index); err != nil {
			return "", err
		}
		uploads := append([]artifact{}, files...)
		uploads = append(uploads, artifact{"index.yaml", yamlData}, artifact{"index.html", html.Bytes()}, artifact{".nojekyll", []byte{}})
		var treeEntries []map[string]string
		for _, file := range uploads {
			sha, ok := blobs[file.path]
			if !ok || file.path == "index.yaml" || file.path == "index.html" {
				var blob struct {
					SHA string `json:"sha"`
				}
				if err := client.call(ctx, "POST", root+"/git/blobs", map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(file.data)}, &blob); err != nil {
					return "", err
				}
				sha = blob.SHA
				blobs[file.path] = sha
			}
			treeEntries = append(treeEntries, map[string]string{"path": file.path, "mode": "100644", "type": "blob", "sha": sha})
		}
		treeInput := map[string]any{"tree": treeEntries}
		if snap.tree != "" {
			treeInput["base_tree"] = snap.tree
		}
		var tree struct {
			SHA string `json:"sha"`
		}
		if err := client.call(ctx, "POST", root+"/git/trees", treeInput, &tree); err != nil {
			return "", err
		}
		parents := []string{}
		if snap.head != "" {
			parents = append(parents, snap.head)
		}
		var commit struct {
			SHA string `json:"sha"`
		}
		if err := client.call(ctx, "POST", root+"/git/commits", map[string]any{"message": "Publish benchmark report " + report.ID, "tree": tree.SHA, "parents": parents}, &commit); err != nil {
			return "", err
		}
		if snap.head == "" {
			err = client.call(ctx, "POST", root+"/git/refs", map[string]string{"ref": "refs/heads/pages", "sha": commit.SHA}, nil)
		} else {
			err = client.call(ctx, "PATCH", root+"/git/refs/heads/pages", map[string]any{"sha": commit.SHA, "force": false}, nil)
		}
		if err != nil {
			if hasStatus(err, 409) || hasStatus(err, 422) {
				// Retry only if the head actually changed, not for branch protection or
				// validation errors that happen to use the same HTTP status.
				current, checkErr := loadSnapshot(ctx, client, root)
				if checkErr == nil && current.head != snap.head {
					continue
				}
			}
			return "", err
		}
		site, err := configurePages(ctx, client, root)
		if err != nil {
			return "", fmt.Errorf("report committed to %s/pages (commit %s), but Pages configuration failed: %w", root, commit.SHA, err)
		}
		if err := ensureReadme(ctx, client, root, site); err != nil {
			return "", fmt.Errorf("report committed (commit %s) and Pages configured at %s, but default-branch README update failed: %w", commit.SHA, site, err)
		}
		return strings.TrimRight(site, "/") + "/reports/" + url.PathEscape(report.ID) + "/report.html", nil
	}
	return "", errors.New("pages branch kept changing; publication was not committed, retry the command")
}

func validComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func hasStatus(err error, status int) bool {
	var e *apiError
	return errors.As(err, &e) && e.status == status
}

func loadSnapshot(ctx context.Context, client api, root string) (snapshot, error) {
	out := snapshot{files: map[string]string{}}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := client.call(ctx, "GET", root+"/git/ref/heads/pages", nil, &ref)
	if hasStatus(err, 404) || hasStatus(err, 409) {
		refErr := err
		// The Git Data API cannot initialize an entirely empty repository. Bootstrap
		// its default branch through Contents, then create an independent pages root.
		var repo struct {
			Size int `json:"size"`
		}
		if err := client.call(ctx, "GET", root, nil, &repo); err != nil {
			return out, err
		}
		var branches []any
		if repo.Size == 0 || hasStatus(refErr, 409) {
			if err := client.call(ctx, "GET", root+"/branches?per_page=1", nil, &branches); err != nil {
				return out, err
			}
			if len(branches) == 0 {
				if err := client.call(ctx, "PUT", root+"/contents/.nojekyll", map[string]string{"message": "Initialize reports repository", "content": ""}, nil); err != nil {
					return out, err
				}
			} else if hasStatus(refErr, 409) {
				return out, refErr
			}
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.head = ref.Object.SHA
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := client.call(ctx, "GET", root+"/git/commits/"+out.head, nil, &commit); err != nil {
		return out, err
	}
	out.tree = commit.Tree.SHA
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
			Type string `json:"type"`
			Mode string `json:"mode"`
		} `json:"tree"`
	}
	if err := client.call(ctx, "GET", root+"/git/trees/"+out.tree+"?recursive=1", nil, &tree); err != nil {
		return out, err
	}
	if tree.Truncated {
		return out, errors.New("repository tree is too large to safely inspect")
	}
	for _, item := range tree.Tree {
		out.files[item.Path] = item.SHA
		if item.Path == "index.yaml" && (item.Type != "blob" || item.Mode != "100644") {
			return out, errors.New("index.yaml is not a regular file")
		}
		if item.Path == "reports" && item.Type != "tree" {
			return out, errors.New("reports destination is not a directory")
		}
	}
	return out, nil
}

func readCatalogue(ctx context.Context, client api, root string, snap snapshot) (catalogue, error) {
	index := catalogue{Version: 1, Reports: []entry{}}
	sha, ok := snap.files["index.yaml"]
	if !ok {
		return index, nil
	}
	var blob struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := client.call(ctx, "GET", root+"/git/blobs/"+sha, nil, &blob); err != nil {
		return index, err
	}
	if blob.Encoding != "base64" {
		return index, errors.New("unsupported index.yaml blob encoding")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
	if err != nil {
		return index, err
	}
	index = catalogue{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&index); err != nil {
		return index, fmt.Errorf("invalid index.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return index, errors.New("index.yaml must contain exactly one YAML document")
	}
	if index.Version != 1 {
		return index, fmt.Errorf("unsupported index.yaml version %d", index.Version)
	}
	seen := map[string]bool{}
	for _, item := range index.Reports {
		if !evidence.ValidRunName(item.ID) || item.Path != "reports/"+item.ID+"/report.html" || seen[item.ID] {
			return index, errors.New("invalid or duplicate report entry in index.yaml")
		}
		seen[item.ID] = true
	}
	return index, nil
}

func configurePages(ctx context.Context, client api, root string) (string, error) {
	var site struct {
		HTMLURL string `json:"html_url"`
	}
	err := client.call(ctx, "GET", root+"/pages", nil, &site)
	method := "PUT"
	if hasStatus(err, 404) {
		method = "POST"
	} else if err != nil {
		return "", err
	}
	input := map[string]any{"build_type": "legacy", "source": map[string]string{"branch": "pages", "path": "/"}}
	if err := client.call(ctx, method, root+"/pages", input, nil); err != nil {
		return "", err
	}
	if err := client.call(ctx, "GET", root+"/pages", nil, &site); err != nil {
		return "", err
	}
	if site.HTMLURL == "" {
		return "", errors.New("GitHub did not return a Pages URL")
	}
	return site.HTMLURL, nil
}

func prepare(dir string, now time.Time) ([]artifact, entry, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, entry{}, err
	}
	id := filepath.Base(absolute)
	if !evidence.ValidRunName(id) {
		return nil, entry{}, fmt.Errorf("invalid report directory name %q", id)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, entry{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, entry{}, errors.New("report path must be a directory, not a symlink")
	}
	result := entry{ID: id, Path: "reports/" + id + "/report.html", PublishedAt: now}
	var files []artifact
	required := map[string]bool{"report.html": false, "report-local.html": false, "plan.yaml": false, "status.yaml": false}
	err = filepath.WalkDir(absolute, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in report input: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		_, metadata := required[rel]
		parts := strings.Split(rel, "/")
		profile := len(parts) == 3 && (parts[0] == "baseline" || parts[0] == "comparison") && (parts[2] == "cpu.pprof" || parts[2] == "heap.pprof")
		if !metadata && !profile {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 64<<20 {
			return fmt.Errorf("invalid or oversized report input: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if metadata {
			required[rel] = true
		}
		switch rel {
		case "plan.yaml":
			var plan evidence.Plan
			if err := yaml.Unmarshal(data, &plan); err != nil {
				return fmt.Errorf("invalid plan.yaml: %w", err)
			}
			result.Baseline, result.Comparison = plan.Baseline, plan.Comparison
		case "status.yaml":
			var status evidence.Status
			if err := yaml.Unmarshal(data, &status); err != nil {
				return fmt.Errorf("invalid status.yaml: %w", err)
			}
			result.StartedAt = status.StartedAt
		default:
			if metadata && len(data) == 0 {
				return fmt.Errorf("empty report: %s", rel)
			}
			files = append(files, artifact{"reports/" + id + "/" + rel, data})
		}
		return nil
	})
	if err != nil {
		return nil, entry{}, err
	}
	for _, name := range []string{"report.html", "report-local.html", "plan.yaml", "status.yaml"} {
		if !required[name] {
			return nil, entry{}, fmt.Errorf("missing %s in report directory", name)
		}
	}
	return files, result, nil
}

var indexHTML = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>benchstat-prof reports</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:70rem;margin:2rem auto;padding:0 1rem}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:.7rem;border-bottom:1px solid #ccc}code{overflow-wrap:anywhere}</style></head>
<body><h1>benchstat-prof reports</h1><table><thead><tr><th>Report</th><th>Published (UTC)</th><th>Baseline</th><th>Comparison</th></tr></thead><tbody>
{{range .Reports}}<tr><td><a href="{{.Path}}">{{.ID}}</a></td><td>{{.PublishedAt.UTC.Format "2006-01-02 15:04:05"}}</td><td>{{.Baseline.Ref}}<br><code>{{.Baseline.Commit}}</code></td><td>{{.Comparison.Ref}}<br><code>{{.Comparison.Commit}}</code></td></tr>{{end}}
</tbody></table></body></html>`))
