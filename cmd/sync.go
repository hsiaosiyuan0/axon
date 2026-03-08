package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	axsync "github.com/hsiaosiyuan0/axon/internal/sync"
	"github.com/spf13/cobra"
)

var (
	syncDirection  string
	syncRemotePath string
	syncVerbose    bool
	syncBackend    string
	// WebDAV
	syncWebDAVURL      string
	syncWebDAVUser     string
	syncWebDAVPassword string
	// S3
	syncS3Endpoint  string
	syncS3Bucket    string
	syncS3AccessKey string
	syncS3SecretKey string
	syncS3Region    string
	// Local
	syncLocalDir string
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync your knowledge base to/from a remote backend",
	Long: `Synchronize the Axon DB file with a remote storage backend.

Supported backends:
  webdav   Nextcloud, ownCloud, or any WebDAV server
  s3       AWS S3, MinIO, Cloudflare R2, or any S3-compatible store
  local    Local directory (NFS, USB drive, etc.)

Direction:
  auto     Compare timestamps and sync the newer version (default)
  push     Always upload local → remote
  pull     Always download remote → local

Environment variables (alternative to flags):
  AXON_SYNC_BACKEND       webdav | s3 | local
  AXON_WEBDAV_URL         WebDAV base URL
  AXON_WEBDAV_USER        WebDAV username
  AXON_WEBDAV_PASSWORD    WebDAV password
  AXON_S3_ENDPOINT        S3 endpoint URL
  AXON_S3_BUCKET          S3 bucket name
  AXON_S3_ACCESS_KEY      S3 access key
  AXON_S3_SECRET_KEY      S3 secret key
  AXON_S3_REGION          S3 region (default: us-east-1)
  AXON_SYNC_LOCAL_DIR     Local directory for local backend

Examples:
  # WebDAV (Nextcloud)
  axon sync --backend webdav \
    --webdav-url https://cloud.example.com/remote.php/dav/files/alice/ \
    --webdav-user alice --webdav-password s3cret

  # S3 / MinIO
  axon sync --backend s3 \
    --s3-endpoint http://localhost:9000 \
    --s3-bucket my-backup \
    --s3-access-key minioadmin --s3-secret-key minioadmin

  # Push only
  axon sync --backend webdav ... --direction push

  # Env-based (no flags needed)
  AXON_SYNC_BACKEND=webdav AXON_WEBDAV_URL=... axon sync`,
	RunE: runSync,
}

func init() {
	syncCmd.Flags().StringVar(&syncDirection, "direction", "auto", "Sync direction: auto, push, pull")
	syncCmd.Flags().StringVar(&syncRemotePath, "remote-path", "axon/axon.db", "Remote object path/key")
	syncCmd.Flags().BoolVarP(&syncVerbose, "verbose", "v", false, "Verbose output")
	syncCmd.Flags().StringVar(&syncBackend, "backend", "", "Backend: webdav, s3, local")

	// WebDAV
	syncCmd.Flags().StringVar(&syncWebDAVURL, "webdav-url", "", "WebDAV base URL")
	syncCmd.Flags().StringVar(&syncWebDAVUser, "webdav-user", "", "WebDAV username")
	syncCmd.Flags().StringVar(&syncWebDAVPassword, "webdav-password", "", "WebDAV password")

	// S3
	syncCmd.Flags().StringVar(&syncS3Endpoint, "s3-endpoint", "", "S3 endpoint URL")
	syncCmd.Flags().StringVar(&syncS3Bucket, "s3-bucket", "", "S3 bucket name")
	syncCmd.Flags().StringVar(&syncS3AccessKey, "s3-access-key", "", "S3 access key")
	syncCmd.Flags().StringVar(&syncS3SecretKey, "s3-secret-key", "", "S3 secret key")
	syncCmd.Flags().StringVar(&syncS3Region, "s3-region", "us-east-1", "S3 region")

	// Local
	syncCmd.Flags().StringVar(&syncLocalDir, "local-dir", "", "Local directory for local backend")
}

func runSync(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(globalDB)
	if err != nil {
		return err
	}

	// Resolve backend from flag or env
	backend := orEnv(syncBackend, "AXON_SYNC_BACKEND")
	if backend == "" {
		return fmt.Errorf("--backend is required (webdav, s3, or local)\n\nRun 'axon sync --help' for usage examples")
	}

	var b axsync.Backend

	switch strings.ToLower(backend) {
	case "webdav":
		url := orEnv(syncWebDAVURL, "AXON_WEBDAV_URL")
		user := orEnv(syncWebDAVUser, "AXON_WEBDAV_USER")
		pass := orEnv(syncWebDAVPassword, "AXON_WEBDAV_PASSWORD")
		if url == "" {
			return fmt.Errorf("WebDAV URL is required (--webdav-url or AXON_WEBDAV_URL)")
		}
		b = axsync.NewWebDAV(url, user, pass)

	case "s3":
		endpoint := orEnv(syncS3Endpoint, "AXON_S3_ENDPOINT")
		bucket := orEnv(syncS3Bucket, "AXON_S3_BUCKET")
		accessKey := orEnv(syncS3AccessKey, "AXON_S3_ACCESS_KEY")
		secretKey := orEnv(syncS3SecretKey, "AXON_S3_SECRET_KEY")
		region := orEnv(syncS3Region, "AXON_S3_REGION")
		if endpoint == "" {
			endpoint = "https://s3.amazonaws.com"
		}
		if bucket == "" {
			return fmt.Errorf("S3 bucket is required (--s3-bucket or AXON_S3_BUCKET)")
		}
		b = axsync.NewS3(endpoint, bucket, accessKey, secretKey, region)

	case "local":
		dir := orEnv(syncLocalDir, "AXON_SYNC_LOCAL_DIR")
		if dir == "" {
			return fmt.Errorf("local directory is required (--local-dir or AXON_SYNC_LOCAL_DIR)")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating local dir: %w", err)
		}
		b = &axsync.LocalBackend{Dir: dir}

	default:
		return fmt.Errorf("unknown backend %q — use webdav, s3, or local", backend)
	}

	opts := axsync.SyncOptions{
		LocalPath:  cfg.DBPath,
		RemotePath: syncRemotePath,
		Direction:  syncDirection,
		Verbose:    syncVerbose,
	}

	fmt.Printf("🔄 Syncing knowledge base…\n")
	fmt.Printf("   Local : %s\n", cfg.DBPath)
	fmt.Printf("   Remote: %s → %s\n", b.Name(), syncRemotePath)
	fmt.Printf("   Mode  : %s\n\n", syncDirection)

	result, err := axsync.Run(cmd.Context(), b, opts)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	switch result.Action {
	case "uploaded":
		fmt.Printf("✅ Uploaded  (%d bytes, md5: %s)\n", result.Bytes, result.LocalMD5)
	case "downloaded":
		fmt.Printf("✅ Downloaded (%d bytes, md5: %s)\n", result.Bytes, result.RemoteMD5)
	case "already-in-sync":
		fmt.Printf("✅ Already in sync (md5: %s)\n", result.LocalMD5)
	default:
		fmt.Printf("✅ Done: %s\n", result.Action)
	}
	return nil
}

// orEnv returns flag value if non-empty, otherwise the value of the env var.
func orEnv(flag, envKey string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(envKey)
}
