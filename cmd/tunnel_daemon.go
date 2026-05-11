package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/git-treeline/cli/internal/tunneldaemon"
	"github.com/spf13/cobra"
)

var (
	tunnelDaemonSocket string
	tunnelDaemonName   string
)

func init() {
	tunnelDaemonCmd.Flags().StringVar(&tunnelDaemonSocket, "socket", "", "Unix socket path to listen on (required)")
	tunnelDaemonCmd.Flags().StringVar(&tunnelDaemonName, "tunnel", "", "Cloudflare tunnel name to manage (required)")
	rootCmd.AddCommand(tunnelDaemonCmd)
}

// tunnelDaemonCmd is hidden: it's spawned by `gtl tunnel` when no daemon is
// running for the requested tunnel. Users shouldn't run it directly.
var tunnelDaemonCmd = &cobra.Command{
	Use:    "tunnel-daemon",
	Hidden: true,
	Short:  "Internal: run the gtl tunnel multiplexing daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		if tunnelDaemonSocket == "" || tunnelDaemonName == "" {
			return fmt.Errorf("--socket and --tunnel are required")
		}

		if err := tunneldaemon.EnsureSocketDir(); err != nil {
			return fmt.Errorf("prepare socket dir: %w", err)
		}

		// Best-effort cleanup of a stale socket from a prior crashed daemon.
		// If another daemon is alive on this socket, Listen will fail and we
		// exit, which is the correct behaviour for a lost spawn race.
		if _, err := os.Stat(tunnelDaemonSocket); err == nil {
			if c, dialErr := net.Dial("unix", tunnelDaemonSocket); dialErr == nil {
				_ = c.Close()
				// Another daemon is already serving this socket. Exit silently.
				return nil
			}
			_ = os.Remove(tunnelDaemonSocket)
		}

		// Tighten umask so the socket is created 0600 atomically (no
		// TOCTOU window between net.Listen and a follow-up Chmod).
		prevUmask := syscall.Umask(0o077)
		ln, err := net.Listen("unix", tunnelDaemonSocket)
		syscall.Umask(prevUmask)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		defer func() {
			_ = ln.Close()
			_ = os.Remove(tunnelDaemonSocket)
		}()

		d := tunneldaemon.New(tunnelDaemonName)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			select {
			case <-d.Done():
				cancel()
			case <-sigs:
				cancel()
			}
		}()

		return d.Run(ctx, ln)
	},
}
