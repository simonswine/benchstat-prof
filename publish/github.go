// SPDX-License-Identifier: Apache-2.0
package publish

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

type api interface {
	call(context.Context, string, string, any, any) error
}

type ghAPI struct{}

type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

// gh supplies authentication; neither tokens nor local Git configuration are read.
func (ghAPI) call(ctx context.Context, method, endpoint string, input, output any) error {
	args := []string{"api", "--hostname", "github.com", "--include", "--method", method, endpoint}
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
		args = append(args, "--input", "-")
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	response, err := http.ReadResponse(bufio.NewReader(&stdout), nil)
	if err != nil {
		if runErr != nil {
			return fmt.Errorf("gh api %s %s: %w: %s", method, endpoint, runErr, strings.TrimSpace(stderr.String()))
		}
		return fmt.Errorf("decode gh api response: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &detail)
		return &apiError{response.StatusCode, fmt.Sprintf("gh api %s %s: HTTP %d: %s", method, endpoint, response.StatusCode, detail.Message)}
	}
	if runErr != nil {
		return fmt.Errorf("gh api: %w: %s", runErr, stderr.String())
	}
	if output != nil {
		return json.Unmarshal(data, output)
	}
	return nil
}
