package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/watch"
	"github.com/spf13/cobra"
)

// ── flags ──────────────────────────────────────────────────────────────────

var (
	watchCollection string
	watchExts       string
	watchInterval   time.Duration
	watchIgnoreDots bool
	watchVerbose    bool
	watchDaemon     bool
	watchLogFile    string
	watchPIDFile    string
	watchVaultMode  bool // treat dirs as Obsidian vaults (parse wikilinks)
)

// ── root watch command ─────────────────────────────────────────────────────

var watchCmd = &cobra.Command{
	Use:   "watch <dir> [dir2...]",
	Short: "Watch directories for changes and auto-ingest",
	Long: `Watch one or more directories for file changes and automatically ingest
new or modified files into your knowledge base. Deleted files are removed.

Supported events:
  - Created  → axon add <file>
  - Modified → remove old chunks + re-ingest
  - Deleted  → remove source and all its chunks

Changes are debounced: a burst of rapid saves (common in editors) produces
only a single ingest event, fired 2 s after the file settles.

Examples:
  axon watch ~/notes/
  axon watch ~/notes/ ~/projects/ -c work --ext .md,.txt
  axon watch ~/notes/ --daemon
  axon watch stop`,
	Args: cobra.MinimumNArgs(1),
	RunE: runWatch,
}

// watchStopCmd stops the background daemon.
var watchStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background watch daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		pidFile := watchPIDFile
		if pidFile == "" {
			pidFile = watch.DefaultPIDPath()
		}
		return watch.StopDaemon(pidFile)
	},
}

// watchStatusCmd shows daemon status.
var watchStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show watch daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		pidFile := watchPIDFile
		if pidFile == "" {
			pidFile = watch.DefaultPIDPath()
		}
		pid, running := watch.IsRunning(pidFile)
		if running {
			fmt.Printf("✅ Watch daemon is running (PID %d)\n", pid)
			logPath := watch.DefaultLogPath()
			fmt.Printf("   Log: %s\n", logPath)
		} else {
			fmt.Println("💤 Watch daemon is not running")
		}
		return nil
	},
}

func init() {
	watchCmd.Flags().StringVarP(&watchCollection, "collection", "c", "", "Target collection (name or ID); empty = auto-classify")
	watchCmd.Flags().StringVar(&watchExts, "ext", ".md,.txt,.pdf,.html", "Comma-separated file extensions to watch")
	watchCmd.Flags().DurationVar(&watchInterval, "interval", 3*time.Second, "Poll interval (e.g. 3s, 10s, 1m)")
	watchCmd.Flags().BoolVar(&watchIgnoreDots, "ignore-dotfiles", true, "Skip hidden files and directories")
	watchCmd.Flags().BoolVarP(&watchVerbose, "verbose", "v", false, "Show detailed event info")
	watchCmd.Flags().BoolVar(&watchDaemon, "daemon", false, "Run in background (writes PID to ~/.axon/watch.pid)")
	watchCmd.Flags().StringVar(&watchLogFile, "log", "", "Log file path (default: ~/.axon/watch.log)")
	watchCmd.Flags().StringVar(&watchPIDFile, "pid", "", "PID file path (default: ~/.axon/watch.pid)")
	watchCmd.Flags().BoolVar(&watchVaultMode, "vault", false, "Treat dirs as Obsidian vaults (show wikilink relation count)")

	// Propagate --pid flag to sub-commands too.
	watchStopCmd.Flags().StringVar(&watchPIDFile, "pid", "", "PID file path (default: ~/.axon/watch.pid)")
	watchStatusCmd.Flags().StringVar(&watchPIDFile, "pid", "", "PID file path (default: ~/.axon/watch.pid)")

	watchCmd.AddCommand(watchStopCmd)
	watchCmd.AddCommand(watchStatusCmd)
}

// ── runWatch ───────────────────────────────────────────────────────────────

func runWatch(cmd *cobra.Command, args []string) error {
	// Resolve PID / log paths.
	pidFile := watchPIDFile
	if pidFile == "" {
		pidFile = watch.DefaultPIDPath()
	}
	logPath := watchLogFile
	if logPath == "" {
		logPath = watch.DefaultLogPath()
	}

	// --daemon: re-exec self in background.
	if watchDaemon {
		return spawnDaemon(args, pidFile, logPath)
	}

	// Check for already-running daemon.
	if pid, running := watch.IsRunning(pidFile); running {
		fmt.Printf("⚠️  Watch daemon already running (PID %d). Use 'axon watch stop' first.\n", pid)
		return nil
	}

	// If stdout is a terminal we're in foreground mode; no log redirect.
	// If we were spawned as daemon (no terminal), redirect to log file.
	var logWriter io.Writer = os.Stdout
	isDaemon := !isTerminal()
	if isDaemon {
		lf, err := watch.OpenLogFile(logPath)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer lf.Close()
		logWriter = lf

		// Write PID file.
		if err := watch.WritePID(pidFile); err != nil {
			return fmt.Errorf("write PID: %w", err)
		}
		defer watch.RemovePID(pidFile)
	}

	// Parse extensions.
	var exts []string
	for _, e := range strings.Split(watchExts, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts = append(exts, e)
	}

	// Validate directories (expand ~).
	dirs := make([]string, 0, len(args))
	for _, d := range args {
		d = expandHome(d)
		info, err := os.Stat(d)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("not a directory: %s", d)
		}
		dirs = append(dirs, d)
	}

	cfg, err := config.Load(globalDB)
	if err != nil {
		return err
	}
	svc, err := ingest.NewService(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	w := watch.New(watch.Config{
		Dirs:           dirs,
		Exts:           exts,
		Interval:       watchInterval,
		IgnoreDotfiles: watchIgnoreDots,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logf(logWriter, "\n🛑 Received %v, stopping watch...\n", sig)
		cancel()
	}()

	// Event consumer.
	go func() {
		for event := range w.Events {
			handleWatchEvent(ctx, logWriter, svc, event, watchCollection, watchVerbose, watchVaultMode)
		}
	}()

	if isDaemon {
		logf(logWriter, "🚀 Watch daemon started (PID %d)\n", os.Getpid())
		logf(logWriter, "   Dirs:     %s\n", strings.Join(dirs, ", "))
		logf(logWriter, "   Interval: %v\n", watchInterval)
		logf(logWriter, "   Log:      %s\n", logPath)
	}

	return w.Run(ctx)
}

// spawnDaemon re-execs the current binary with the same arguments but
// without --daemon, redirecting stdout/stderr to the log file.
func spawnDaemon(dirs []string, pidFile, logPath string) error {
	// Check already running.
	if pid, running := watch.IsRunning(pidFile); running {
		fmt.Printf("⚠️  Watch daemon already running (PID %d)\n", pid)
		return nil
	}

	// Open log file for the child.
	lf, err := watch.OpenLogFile(logPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	// Build child argv: same binary, same sub-command args, minus --daemon.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := []string{"watch"}
	childArgs = append(childArgs, dirs...)
	if watchCollection != "" {
		childArgs = append(childArgs, "-c", watchCollection)
	}
	childArgs = append(childArgs, "--ext", watchExts)
	childArgs = append(childArgs, "--interval", watchInterval.String())
	if !watchIgnoreDots {
		childArgs = append(childArgs, "--ignore-dotfiles=false")
	}
	if watchVerbose {
		childArgs = append(childArgs, "-v")
	}
	if watchVaultMode {
		childArgs = append(childArgs, "--vault")
	}
	if watchPIDFile != "" {
		childArgs = append(childArgs, "--pid", watchPIDFile)
	}
	if watchLogFile != "" {
		childArgs = append(childArgs, "--log", watchLogFile)
	}
	// Pass --db flag if set (globalDB is the flag value from root cmd).
	if globalDB != "" {
		childArgs = append([]string{"--db", globalDB}, childArgs...)
	}

	c := exec.Command(self, childArgs...)
	c.Stdout = lf
	c.Stderr = lf
	c.Stdin = nil
	// Detach from current session.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := c.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	fmt.Printf("🚀 Watch daemon started (PID %d)\n", c.Process.Pid)
	fmt.Printf("   Log:  %s\n", logPath)
	fmt.Printf("   PID:  %s\n", pidFile)
	fmt.Printf("   Stop: axon watch stop\n")
	return nil
}

// ── handleWatchEvent ───────────────────────────────────────────────────────

func handleWatchEvent(ctx context.Context, w io.Writer, svc *ingest.Service, event watch.Event, collection string, verbose bool, vaultMode bool) {
	ts := time.Now().Format("15:04:05")
	switch event.Type {
	case watch.EventCreated:
		logf(w, "[%s] ➕ created  %s\n", ts, event.Path)
		result, err := svc.Add(ctx, ingest.AddOptions{
			Origin:     event.Path,
			Collection: collection,
		})
		if err != nil {
			logf(w, "         ⚠️  Failed: %v\n", err)
			return
		}
		if vaultMode && result.RelationCount > 0 {
			logf(w, "         ✅ %d chunks, %d relations → %s\n", result.ChunkCount, result.RelationCount, result.Collection)
		} else {
			logf(w, "         ✅ %d chunks → %s\n", result.ChunkCount, result.Collection)
		}

	case watch.EventModified:
		logf(w, "[%s] ✏️  modified %s\n", ts, event.Path)
		if err := svc.Remove(event.Path); err != nil && verbose {
			logf(w, "         ⚠️  cleanup: %v\n", err)
		}
		result, err := svc.Add(ctx, ingest.AddOptions{
			Origin:     event.Path,
			Collection: collection,
		})
		if err != nil {
			logf(w, "         ⚠️  Re-ingest failed: %v\n", err)
			return
		}
		if vaultMode && result.RelationCount > 0 {
			logf(w, "         ✅ updated %d chunks, %d relations → %s\n", result.ChunkCount, result.RelationCount, result.Collection)
		} else {
			logf(w, "         ✅ updated %d chunks → %s\n", result.ChunkCount, result.Collection)
		}

	case watch.EventDeleted:
		logf(w, "[%s] 🗑️  deleted  %s\n", ts, event.Path)
		if err := svc.Remove(event.Path); err != nil {
			logf(w, "         ⚠️  Removal failed: %v\n", err)
			return
		}
		logf(w, "         ✅ removed from knowledge base\n")
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func logf(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// isTerminal returns true when stdout is connected to a terminal.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
