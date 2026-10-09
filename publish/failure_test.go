// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type apiFunc func(context.Context, string, string, any, any) error

func (f apiFunc) call(ctx context.Context, method, endpoint string, input, output any) error {
	return f(ctx, method, endpoint, input, output)
}
func respond(output, result any) error {
	if output == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

func TestEmptyRepository(t *testing.T) {
	f := newFake()
	f.head = ""
	empty := true
	client := apiFunc(func(ctx context.Context, method, endpoint string, input, output any) error {
		root := "repos/alice/benchstat-prof-reports"
		if empty {
			switch endpoint {
			case root:
				return respond(output, map[string]any{"size": 0, "default_branch": "main"})
			case root + "/git/ref/heads/pages":
				return &apiError{409, "Git Repository is empty."}
			case root + "/branches?per_page=1":
				return respond(output, []any{})
			case root + "/contents/.nojekyll":
				if method != "PUT" {
					t.Fatal("not a Contents bootstrap")
				}
				empty = false
				return nil
			}
		}
		return f.call(ctx, method, endpoint, input, output)
	})
	if _, err := run(context.Background(), client, fixture(t), Options{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if empty || f.head == "" {
		t.Fatal("empty repository not initialized")
	}
}
func TestAPIFailures(t *testing.T) {
	for _, point := range []string{"repository", "blob", "pages", "readme"} {
		t.Run(point, func(t *testing.T) {
			f := newFake()
			client := apiFunc(func(ctx context.Context, method, endpoint string, input, output any) error {
				root := "repos/alice/benchstat-prof-reports"
				fail := point == "repository" && endpoint == root || point == "blob" && endpoint == root+"/git/blobs" || point == "pages" && endpoint == root+"/pages" || point == "readme" && strings.Contains(endpoint, "/contents/README.md")
				if fail {
					return &apiError{403, "permission denied"}
				}
				return f.call(ctx, method, endpoint, input, output)
			})
			_, err := run(context.Background(), client, fixture(t), Options{}, time.Now())
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatal(err)
			}
			if point == "repository" && strings.Contains(err.Error(), "--init") {
				t.Fatal("permission error mistaken for missing repository")
			}
			if point == "pages" || point == "readme" {
				if f.head != "new-commit" || !strings.Contains(err.Error(), "committed") {
					t.Fatal("partial commit not reported", err)
				}
			} else if f.head != "head" {
				t.Fatal("committed after API failure")
			}
		})
	}
}
func TestProtectedBranchDoesNotRetry(t *testing.T) {
	f := newFake()
	updates := 0
	client := apiFunc(func(ctx context.Context, method, endpoint string, input, output any) error {
		if method == "PATCH" {
			updates++
			return &apiError{422, "protected branch"}
		}
		return f.call(ctx, method, endpoint, input, output)
	})
	if _, err := run(context.Background(), client, fixture(t), Options{}, time.Now()); err == nil {
		t.Fatal("ignored protection")
	}
	if updates != 1 {
		t.Fatal("retried unchanged protected branch")
	}
}
