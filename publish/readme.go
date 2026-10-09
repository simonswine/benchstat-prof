// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const readmeStart = "<!-- benchstat-prof:reports:start -->"
const readmeEnd = "<!-- benchstat-prof:reports:end -->"

// Keep the repository landing page useful without replacing a user's README.
// Use Contents rather than a local Git checkout, and its SHA for concurrency.
func ensureReadme(ctx context.Context, client api, root, site string) error {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := client.call(ctx, "GET", root, nil, &repo); err != nil {
		return err
	}
	if repo.DefaultBranch == "" {
		return errors.New("GitHub did not return a default branch")
	}
	section := readmeStart + "\n## Benchmark reports\n\n[View the reports on GitHub Pages](" + strings.TrimRight(site, "/") + "/).\n\nGenerated with [benchstat-prof](https://github.com/simonswine/benchstat-prof).\n" + readmeEnd
	for attempt := 0; attempt < 3; attempt++ {
		var file struct {
			SHA      string `json:"sha"`
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Type     string `json:"type"`
		}
		err := client.call(ctx, "GET", root+"/contents/README.md?ref="+url.QueryEscape(repo.DefaultBranch), nil, &file)
		text := ""
		if err != nil && !hasStatus(err, 404) {
			return err
		}
		if err == nil {
			if file.Type != "file" || file.Encoding != "base64" {
				return errors.New("default-branch README.md is not a regular base64 file")
			}
			data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
			if err != nil {
				return err
			}
			text = string(data)
		}
		updated, err := updateReadme(text, section)
		if err != nil {
			return err
		}
		if updated == text {
			return nil
		}
		input := map[string]string{"message": "Link published benchmark reports", "branch": repo.DefaultBranch, "content": base64.StdEncoding.EncodeToString([]byte(updated))}
		if file.SHA != "" {
			input["sha"] = file.SHA
		}
		err = client.call(ctx, "PUT", root+"/contents/README.md", input, nil)
		if err == nil {
			return nil
		}
		if !hasStatus(err, 409) && !hasStatus(err, 422) {
			return err
		}
	}
	return errors.New("default-branch README kept changing; retry the README update")
}

func updateReadme(text, section string) (string, error) {
	start, end := strings.Index(text, readmeStart), strings.Index(text, readmeEnd)
	if start == -1 && end == -1 {
		if text == "" {
			return "# Benchmark reports\n\n" + section + "\n", nil
		}
		return strings.TrimRight(text, "\n") + "\n\n" + section + "\n", nil
	}
	if start == -1 || end < start || strings.Count(text, readmeStart) != 1 || strings.Count(text, readmeEnd) != 1 {
		return "", fmt.Errorf("invalid benchstat-prof section markers in README.md; leaving it unchanged")
	}
	return text[:start] + section + text[end+len(readmeEnd):], nil
}
