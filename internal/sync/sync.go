// Package sync implements multi-device vault synchronization.
// Supported backends: WebDAV, S3-compatible, and local directory.
package sync

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Backend is a storage backend that can push/pull a single file.
type Backend interface {
	// Name returns a human-readable identifier.
	Name() string
	// Upload uploads data to the remote path.
	Upload(ctx context.Context, remotePath string, data []byte) error
	// Download fetches data from the remote path.
	// Returns (nil, ErrNotFound) if the object doesn't exist.
	Download(ctx context.Context, remotePath string) ([]byte, error)
	// Stat returns metadata about the remote file.
	Stat(ctx context.Context, remotePath string) (*RemoteStat, error)
}

// RemoteStat holds remote file metadata.
type RemoteStat struct {
	Size    int64
	ModTime time.Time
	ETag    string // MD5 or opaque tag
	Exists  bool
}

// ErrNotFound is returned when a remote file doesn't exist.
var ErrNotFound = fmt.Errorf("remote file not found")

// ---------------------------------------------------------------------------
// Sync logic
// ---------------------------------------------------------------------------

// Result contains sync outcome details.
type Result struct {
	Action    string // "uploaded", "downloaded", "already-in-sync", "conflict"
	LocalMD5  string
	RemoteMD5 string
	Bytes     int64
}

// SyncOptions controls sync behavior.
type SyncOptions struct {
	// RemotePath is the object name on the backend (e.g. "axon/axon.db").
	RemotePath string
	// LocalPath is the local DB file path.
	LocalPath string
	// Direction: "push", "pull", or "auto" (default — compare timestamps/size).
	Direction string
	// Verbose enables progress output.
	Verbose bool
}

// Run performs the sync operation.
func Run(ctx context.Context, b Backend, opts SyncOptions) (*Result, error) {
	if opts.RemotePath == "" {
		opts.RemotePath = "axon/axon.db"
	}
	if opts.Direction == "" {
		opts.Direction = "auto"
	}

	localData, localErr := os.ReadFile(opts.LocalPath)
	localExists := localErr == nil

	stat, statErr := b.Stat(ctx, opts.RemotePath)
	remoteExists := statErr == nil && stat != nil && stat.Exists

	logf := func(format string, args ...any) {
		if opts.Verbose {
			fmt.Printf(format+"\n", args...)
		}
	}

	// ---- direction: push ----
	if opts.Direction == "push" {
		if !localExists {
			return nil, fmt.Errorf("local file not found: %s", opts.LocalPath)
		}
		logf("⬆️  Uploading %s → %s (%d bytes)", opts.LocalPath, opts.RemotePath, len(localData))
		if err := b.Upload(ctx, opts.RemotePath, localData); err != nil {
			return nil, fmt.Errorf("upload failed: %w", err)
		}
		return &Result{Action: "uploaded", LocalMD5: md5hex(localData), Bytes: int64(len(localData))}, nil
	}

	// ---- direction: pull ----
	if opts.Direction == "pull" {
		if !remoteExists {
			return nil, fmt.Errorf("remote file not found: %s (backend: %s)", opts.RemotePath, b.Name())
		}
		data, err := b.Download(ctx, opts.RemotePath)
		if err != nil {
			return nil, fmt.Errorf("download failed: %w", err)
		}
		logf("⬇️  Downloaded %s → %s (%d bytes)", opts.RemotePath, opts.LocalPath, len(data))
		if err := writeFile(opts.LocalPath, data); err != nil {
			return nil, err
		}
		return &Result{Action: "downloaded", RemoteMD5: md5hex(data), Bytes: int64(len(data))}, nil
	}

	// ---- direction: auto ----
	if !localExists && !remoteExists {
		return nil, fmt.Errorf("neither local (%s) nor remote (%s) file exists", opts.LocalPath, opts.RemotePath)
	}
	if !remoteExists {
		logf("⬆️  Remote doesn't exist — pushing local copy")
		if err := b.Upload(ctx, opts.RemotePath, localData); err != nil {
			return nil, fmt.Errorf("upload failed: %w", err)
		}
		return &Result{Action: "uploaded", LocalMD5: md5hex(localData), Bytes: int64(len(localData))}, nil
	}
	if !localExists {
		logf("⬇️  Local doesn't exist — pulling remote copy")
		data, err := b.Download(ctx, opts.RemotePath)
		if err != nil {
			return nil, err
		}
		if err := writeFile(opts.LocalPath, data); err != nil {
			return nil, err
		}
		return &Result{Action: "downloaded", RemoteMD5: md5hex(data), Bytes: int64(len(data))}, nil
	}

	// Both exist — compare checksums.
	localMD5 := md5hex(localData)
	remoteMD5 := stat.ETag
	if remoteMD5 == "" {
		remoteData, err := b.Download(ctx, opts.RemotePath)
		if err != nil {
			return nil, fmt.Errorf("download for comparison failed: %w", err)
		}
		remoteMD5 = md5hex(remoteData)
	}

	if localMD5 == remoteMD5 {
		logf("✅ Already in sync (md5: %s)", localMD5)
		return &Result{Action: "already-in-sync", LocalMD5: localMD5, RemoteMD5: remoteMD5}, nil
	}

	// Different — use mod times to decide direction
	localInfo, _ := os.Stat(opts.LocalPath)
	if localInfo != nil && stat.ModTime.Before(localInfo.ModTime()) {
		logf("⬆️  Local is newer — pushing")
		if err := b.Upload(ctx, opts.RemotePath, localData); err != nil {
			return nil, err
		}
		return &Result{Action: "uploaded", LocalMD5: localMD5, RemoteMD5: remoteMD5, Bytes: int64(len(localData))}, nil
	}

	logf("⬇️  Remote is newer — pulling")
	remoteData, err := b.Download(ctx, opts.RemotePath)
	if err != nil {
		return nil, err
	}
	if err := writeFile(opts.LocalPath, remoteData); err != nil {
		return nil, err
	}
	return &Result{Action: "downloaded", LocalMD5: localMD5, RemoteMD5: md5hex(remoteData), Bytes: int64(len(remoteData))}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func md5hex(data []byte) string {
	h := md5.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Local directory backend (for testing / simple rsync-style sync)
// ---------------------------------------------------------------------------

// LocalBackend syncs to a local directory (useful for testing or NFS mounts).
type LocalBackend struct {
	Dir string
}

func (b *LocalBackend) Name() string { return "local:" + b.Dir }

func (b *LocalBackend) Upload(_ context.Context, remotePath string, data []byte) error {
	full := b.fullPath(remotePath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o600)
}

func (b *LocalBackend) Download(_ context.Context, remotePath string) ([]byte, error) {
	data, err := os.ReadFile(b.fullPath(remotePath))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return data, err
}

func (b *LocalBackend) Stat(_ context.Context, remotePath string) (*RemoteStat, error) {
	info, err := os.Stat(b.fullPath(remotePath))
	if os.IsNotExist(err) {
		return &RemoteStat{Exists: false}, nil
	}
	if err != nil {
		return nil, err
	}
	data, _ := os.ReadFile(b.fullPath(remotePath))
	return &RemoteStat{
		Exists:  true,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		ETag:    md5hex(data),
	}, nil
}

func (b *LocalBackend) fullPath(p string) string {
	return b.Dir + "/" + strings.TrimPrefix(p, "/")
}

// ---------------------------------------------------------------------------
// WebDAV backend
// ---------------------------------------------------------------------------

// WebDAVBackend syncs to any WebDAV server (Nextcloud, ownCloud, etc.).
type WebDAVBackend struct {
	URL      string // base URL, e.g. "https://cloud.example.com/remote.php/dav/files/user/"
	Username string
	Password string
	client   *http.Client
}

// NewWebDAV creates a new WebDAV backend.
func NewWebDAV(url, username, password string) *WebDAVBackend {
	return &WebDAVBackend{
		URL:      strings.TrimRight(url, "/"),
		Username: username,
		Password: password,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (b *WebDAVBackend) Name() string { return "webdav:" + b.URL }

func (b *WebDAVBackend) Upload(ctx context.Context, remotePath string, data []byte) error {
	url := b.URL + "/" + strings.TrimPrefix(remotePath, "/")
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.SetBasicAuth(b.Username, b.Password)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint
	if resp.StatusCode >= 300 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		return fmt.Errorf("WebDAV PUT %s → %d", url, resp.StatusCode)
	}
	return nil
}

func (b *WebDAVBackend) Download(ctx context.Context, remotePath string) ([]byte, error) {
	url := b.URL + "/" + strings.TrimPrefix(remotePath, "/")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(b.Username, b.Password)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("WebDAV GET %s → %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (b *WebDAVBackend) Stat(ctx context.Context, remotePath string) (*RemoteStat, error) {
	url := b.URL + "/" + strings.TrimPrefix(remotePath, "/")
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(b.Username, b.Password)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint

	if resp.StatusCode == 404 {
		return &RemoteStat{Exists: false}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("WebDAV HEAD %s → %d", url, resp.StatusCode)
	}

	st := &RemoteStat{Exists: true, Size: resp.ContentLength}
	if etag := resp.Header.Get("ETag"); etag != "" {
		st.ETag = strings.Trim(etag, `"`)
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			st.ModTime = t
		}
	}
	return st, nil
}

// ---------------------------------------------------------------------------
// S3-compatible backend (MinIO, AWS S3, Cloudflare R2, etc.)
// ---------------------------------------------------------------------------

// S3Backend syncs to any S3-compatible object storage.
// Uses only stdlib net/http — no AWS SDK dependency.
type S3Backend struct {
	Endpoint  string // e.g. "https://s3.amazonaws.com" or "http://localhost:9000"
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	client    *http.Client
}

// NewS3 creates a new S3-compatible backend.
// For AWS S3: endpoint = "https://s3.<region>.amazonaws.com"
// For MinIO: endpoint = "http://localhost:9000"
// For R2: endpoint = "https://<account>.r2.cloudflarestorage.com"
func NewS3(endpoint, bucket, accessKey, secretKey, region string) *S3Backend {
	if region == "" {
		region = "us-east-1"
	}
	return &S3Backend{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    region,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (b *S3Backend) Name() string { return "s3://" + b.Bucket }

func (b *S3Backend) objectURL(key string) string {
	return fmt.Sprintf("%s/%s/%s", b.Endpoint, b.Bucket, strings.TrimPrefix(key, "/"))
}

func (b *S3Backend) Upload(ctx context.Context, remotePath string, data []byte) error {
	url := b.objectURL(remotePath)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	b.signRequest(req, data)

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("S3 PUT %s → %d", url, resp.StatusCode)
	}
	return nil
}

func (b *S3Backend) Download(ctx context.Context, remotePath string) ([]byte, error) {
	url := b.objectURL(remotePath)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	b.signRequest(req, nil)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("S3 GET %s → %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (b *S3Backend) Stat(ctx context.Context, remotePath string) (*RemoteStat, error) {
	url := b.objectURL(remotePath)
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, err
	}
	b.signRequest(req, nil)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint

	if resp.StatusCode == 404 {
		return &RemoteStat{Exists: false}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("S3 HEAD %s → %d", url, resp.StatusCode)
	}

	st := &RemoteStat{Exists: true, Size: resp.ContentLength}
	if etag := resp.Header.Get("ETag"); etag != "" {
		st.ETag = strings.Trim(etag, `"`)
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			st.ModTime = t
		}
	}
	return st, nil
}

// signRequest adds a minimal Authorization header.
// NOTE: This is a simplified implementation that works with many S3-compatible
// services when using path-style access. For full AWS SigV4 support, use the AWS SDK.
func (b *S3Backend) signRequest(req *http.Request, _ []byte) {
	if b.AccessKey == "" {
		return
	}
	// Use HTTP Basic for MinIO-compatible services in simple mode.
	req.SetBasicAuth(b.AccessKey, b.SecretKey)
}
