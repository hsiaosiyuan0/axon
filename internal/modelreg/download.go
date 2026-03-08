package modelreg

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadOptions configures a model download.
type DownloadOptions struct {
	// Mirror is the base URL of the mirror to use (e.g. "https://hf-mirror.com").
	// Empty means use the default HuggingFace CDN.
	Mirror string

	// Force re-downloads even if the file already exists.
	Force bool

	// Quiet suppresses progress output.
	Quiet bool
}

// DownloadModel downloads the ONNX model + tokenizer for the given ModelSpec
// into destDir/<spec.Name>/.
// Returns the directory path where files were saved.
func DownloadModel(spec *ModelSpec, destDir string, opts DownloadOptions) (string, error) {
	mirrorBase, err := FindMirror(opts.Mirror)
	if err != nil {
		return "", err
	}

	modelDir := filepath.Join(destDir, spec.Name)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return "", fmt.Errorf("create model dir: %w", err)
	}

	tokPath := filepath.Join(modelDir, "tokenizer.json")
	modelPath := filepath.Join(modelDir, "model.onnx")

	// Download tokenizer.json
	tokURL := ResolveFileURL(mirrorBase, spec.HFRepo, spec.TokenizerPath)
	if err := downloadFile(tokURL, tokPath, opts, "tokenizer.json", 0); err != nil {
		return "", fmt.Errorf("download tokenizer: %w", err)
	}

	// Download model.onnx
	modelURL := ResolveFileURL(mirrorBase, spec.HFRepo, spec.OnnxPath)
	if err := downloadFile(modelURL, modelPath, opts, "model.onnx", int64(spec.SizeMB)*1024*1024); err != nil {
		// Try fallback path (without onnx/ prefix)
		fallbackPath := strings.TrimPrefix(spec.OnnxPath, "onnx/")
		if fallbackPath != spec.OnnxPath {
			fallbackURL := ResolveFileURL(mirrorBase, spec.HFRepo, fallbackPath)
			if err2 := downloadFile(fallbackURL, modelPath, opts, "model.onnx", int64(spec.SizeMB)*1024*1024); err2 != nil {
				return "", fmt.Errorf("download model.onnx (tried %s and %s): %w", modelURL, fallbackURL, err2)
			}
		} else {
			return "", fmt.Errorf("download model.onnx: %w", err)
		}
	}

	return modelDir, nil
}

// downloadFile downloads a single file from url to dest.
// If the file already exists and opts.Force is false, it is skipped.
// sizeHint is an approximate file size in bytes (0 = unknown).
func downloadFile(url, dest string, opts DownloadOptions, label string, sizeHint int64) error {
	if !opts.Force {
		if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
			if !opts.Quiet {
				fmt.Printf("   ✅ %-22s already exists, skipping\n", label)
			}
			return nil
		}
	}

	if !opts.Quiet {
		fmt.Printf("   ⬇️  %-22s", label)
		if sizeHint > 0 {
			fmt.Printf(" (~%d MB)", sizeHint/1024/1024)
		}
		fmt.Println()
	}

	client := &http.Client{Timeout: 0} // no timeout for large files
	resp, err := client.Get(url)       //nolint:noctx
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Check for Git LFS pointer
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if isLFSPointer(body) {
		if !opts.Quiet {
			fmt.Printf("   🔀 LFS pointer detected, fetching from LFS store…\n")
		}
		lfsURL := url
		if !strings.Contains(url, "?download=true") {
			lfsURL = url + "?download=true"
		}
		return downloadWithProgress(lfsURL, dest, opts, label)
	}

	// Write directly
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if !opts.Quiet {
		fmt.Printf("   ✅ %-22s saved (%.1f MB)\n", label, float64(len(body))/1024/1024)
	}
	return nil
}

// downloadWithProgress downloads url → dest with a simple byte-count progress.
func downloadWithProgress(url, dest string, opts DownloadOptions, label string) error {
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()

	total := resp.ContentLength
	pr := &progressReader{
		r:     resp.Body,
		total: total,
		label: label,
		quiet: opts.Quiet,
		last:  time.Now(),
	}

	n, err := io.Copy(f, pr)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if !opts.Quiet {
		fmt.Printf("\r   ✅ %-22s saved (%.1f MB)              \n", label, float64(n)/1024/1024)
	}
	return nil
}

// progressReader wraps an io.Reader and prints download progress.
type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	label    string
	quiet    bool
	last     time.Time
	lastRead int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)

	if !p.quiet && time.Since(p.last) > 200*time.Millisecond {
		speed := float64(p.read-p.lastRead) / time.Since(p.last).Seconds()
		p.lastRead = p.read
		p.last = time.Now()

		mb := float64(p.read) / 1024 / 1024
		if p.total > 0 {
			pct := float64(p.read) / float64(p.total) * 100
			fmt.Printf("\r   ⬇️  %-22s %.1f%% (%.1f MB) %.0f KB/s   ",
				p.label, pct, mb, speed/1024)
		} else {
			fmt.Printf("\r   ⬇️  %-22s %.1f MB  %.0f KB/s   ",
				p.label, mb, speed/1024)
		}
	}
	return n, err
}

func isLFSPointer(data []byte) bool {
	return len(data) < 500 && bytes.HasPrefix(data, []byte("version https://git-lfs.github.com/spec/"))
}
