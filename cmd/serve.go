package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/spf13/cobra"
)

var (
	servePort    int
	serveBind    string
	publicOrigin string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the tutorial web server and open the browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		if publicOrigin != "" {
			u, err := url.Parse(publicOrigin)
			if err != nil || u.Hostname() == "" {
				return fmt.Errorf("--public-origin: invalid URL %q", publicOrigin)
			}
			serve.PublicHost = u.Hostname()
		}

		dir, err := config.TutorialsDir()
		if err != nil {
			return err
		}
		srv := serve.NewServer(dir)
		localURL := fmt.Sprintf("http://localhost:%d", servePort)
		displayURL := localURL
		if publicOrigin != "" {
			displayURL = publicOrigin
		}

		// Record the running server so the worker CLI (`lathe work ...`) can find
		// its URL, and clean it up on shutdown. Best-effort: a failed write only
		// means the worker can't auto-discover the server, not that serving fails.
		// Always the loopback URL: the worker connects on the same host `serve`
		// runs on, regardless of what --public-origin a reverse proxy advertises.
		rt := &config.ServeRuntime{URL: localURL, PID: os.Getpid(), Started: time.Now().UTC()}
		if werr := config.WriteServeRuntime(rt); werr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: could not write serve runtime file: %v\n", werr)
		}
		defer func() { _ = config.RemoveServeRuntime() }()

		fmt.Printf("Serving tutorials at %s\n", displayURL)
		// Nudge toward live mode without spawning anything: starting the loop is
		// the user's call (it can't be agent-agnostic or non-metered otherwise —
		// see the worker-bridge note in AGENTS.md).
		fmt.Println("Live mode: run /lathe-work in your coding agent to drive Ask/Verify/Extend here (otherwise the buttons hand you a command to paste).")
		// Skip the browser pop for a public/daemon deploy — there's no desktop
		// session to open a tab in.
		if publicOrigin == "" {
			openBrowser(localURL)
		}

		// Default bind is loopback only: the server is unauthenticated and
		// exposes a destructive delete endpoint, so by default it must never be
		// reachable from other devices on a shared network. --bind widens this
		// deliberately for a reverse-proxied daemon deploy — pair it with
		// --public-origin and keep auth at the proxy, not here.
		httpSrv := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", serveBind, servePort),
			Handler: srv.Handler(),
		}

		// Shut down gracefully on Ctrl-C / SIGTERM so the deferred runtime-file
		// cleanup runs (ListenAndServe alone never returns on a signal).
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		errCh := make(chan error, 1)
		go func() {
			err := httpSrv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()

		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutdownCtx)
		}
	},
}

func openBrowser(url string) {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "linux":
		bin = "xdg-open"
	default:
		return
	}
	if err := exec.Command(bin, url).Start(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: could not open browser: %v\n", err)
	}
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 4242, "port to listen on")
	serveCmd.Flags().StringVar(&serveBind, "bind", "127.0.0.1", "address to listen on (widen only behind a reverse proxy that owns auth)")
	serveCmd.Flags().StringVar(&publicOrigin, "public-origin", "", "public URL this server is reverse-proxied at (e.g. https://lathe.lan); required for state-changing requests to pass the same-origin check when --bind is not loopback")
	rootCmd.AddCommand(serveCmd)
}
