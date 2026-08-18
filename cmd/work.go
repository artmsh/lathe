package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/spf13/cobra"
)

// workCmd groups the worker-loop commands the /lathe-work skill drives. The
// skill long-polls `work next` to claim a job the browser enqueued, applies the
// matching /lathe-* protocol in the interactive session (the binary still never
// drives a model), then reports back via `work answer` (ask) or `work done`
// (verify/extend). All three talk to the running server, discovered through
// ~/.lathe/serve.json, or via --server when the worker runs on a different
// machine than `lathe serve`.
var workCmd = &cobra.Command{
	Use:   "work",
	Short: "Worker-loop commands bridging the web UI and this session (used by the /lathe-work skill)",
}

// workServerURL overrides discovery via --server, for a worker session running
// on a different machine than `lathe serve` — ~/.lathe/serve.json only ever
// holds the server's own loopback URL (see cmd/serve.go), so cross-machine
// setups (serve behind --bind/--public-origin, worker on another VM) have no
// local runtime file to read and must be told the URL explicitly.
var workServerURL string

// serveBaseURL resolves the running server's base URL: --server if set,
// otherwise ~/.lathe/serve.json, returning a clean, actionable error when
// neither is available.
func serveBaseURL() (string, error) {
	if workServerURL != "" {
		return strings.TrimRight(workServerURL, "/"), nil
	}
	rt, err := config.ReadServeRuntime()
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no lathe server is running (start one with `lathe serve`, or pass --server for a remote one)")
		}
		return "", fmt.Errorf("read serve runtime file: %w", err)
	}
	return strings.TrimRight(rt.URL, "/"), nil
}

// workNextLongPoll is the client timeout for `work next`. It sits just above the
// server's 50s long-poll window so the server wins the race and returns a clean
// "no task" rather than the client timing out.
const workNextLongPoll = 55 * time.Second

var workNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Long-poll for the next queued job and print it as JSON (prints `no task` if idle)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := serveBaseURL()
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: workNextLongPoll}
		resp, err := client.Get(base + "/-/work")
		if err != nil {
			return fmt.Errorf("contact lathe server at %s: %w (is `lathe serve` still running?)", base, err)
		}
		defer func() { _ = resp.Body.Close() }()

		switch resp.StatusCode {
		case http.StatusNoContent:
			// No job within the long-poll window — the skill loops and polls again.
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no task")
			return nil
		case http.StatusOK:
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			// Emit the job JSON verbatim for the skill to parse.
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(body)))
			return nil
		default:
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("lathe server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
	},
}

var workAnswerFile string

var workAnswerCmd = &cobra.Command{
	Use:   "answer <id> --answer <path>",
	Short: "Report an ask job's answer back to the browser (use --answer - for stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if workAnswerFile == "" {
			return fmt.Errorf("--answer is required (use --answer - to read the answer from stdin)")
		}
		// readSpec (from voice.go) reads a file path or stdin ("-") — the same
		// pattern `lathe voice add --file -` uses.
		content, err := readSpec(cmd, workAnswerFile)
		if err != nil {
			return err
		}
		answer := strings.TrimRight(string(content), "\n")
		base, err := serveBaseURL()
		if err != nil {
			return err
		}
		return postJSON(base+"/-/work/"+args[0]+"/answer", map[string]string{"answer": answer})
	},
}

var workDoneCmd = &cobra.Command{
	Use:   "done <id>",
	Short: "Mark a verify/extend job complete (after its result was recorded on disk)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := serveBaseURL()
		if err != nil {
			return err
		}
		return postJSON(base+"/-/work/"+args[0]+"/done", nil)
	},
}

// postJSON POSTs payload (or an empty body when nil) to url and treats any 2xx
// as success. The worker endpoints reply 204 No Content.
func postJSON(url string, payload any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contact lathe server: %w (is `lathe serve` still running?)", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lathe server returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func init() {
	workCmd.PersistentFlags().StringVar(&workServerURL, "server", "", "lathe server base URL (e.g. https://lathe.lan); overrides auto-discovery from ~/.lathe/serve.json, for a worker session on a different machine than `lathe serve`")
	workAnswerCmd.Flags().StringVar(&workAnswerFile, "answer", "", "path to the answer markdown, or - for stdin (required)")
	workCmd.AddCommand(workNextCmd, workAnswerCmd, workDoneCmd)
	rootCmd.AddCommand(workCmd)
}
