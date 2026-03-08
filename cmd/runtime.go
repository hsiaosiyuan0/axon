package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// ortVersion must match internal/embed/runtime.go OrtVersion
const ortVersion = "1.21.0"

var initRuntimeCmd = &cobra.Command{
	Use:   "init-runtime",
	Short: "Download the ONNX Runtime shared library required for local embedding",
	Long: `Downloads the platform-appropriate libonnxruntime shared library from
the official Microsoft ONNX Runtime GitHub releases and installs it to
~/.axon/lib/.

This is required when using local ONNX embedding models (--tags onnx build).
The model files themselves are downloaded separately via:

  axon model download bge-small-zh-v1.5

After running init-runtime, rebuild axon with ONNX support:

  CGO_ENABLED=1 go build --tags "fts5 onnx" -o axon .

Examples:
  axon model init-runtime
  axon model init-runtime --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		return runInitRuntime(force)
	},
}

func init() {
	initRuntimeCmd.Flags().Bool("force", false, "Re-download even if library already exists")
	modelCmd.AddCommand(initRuntimeCmd)
}

func runInitRuntime(force bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	libDir := filepath.Join(home, ".axon", "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return fmt.Errorf("create lib dir: %w", err)
	}

	libName, archiveName, downloadURL, err := ortAssetInfo()
	if err != nil {
		return err
	}

	destPath := filepath.Join(libDir, libName)

	if !force {
		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("✅ ONNX Runtime already installed: %s\n", destPath)
			fmt.Printf("   (use --force to re-download)\n")
			printNextSteps(destPath)
			return nil
		}
	}

	fmt.Printf("🖥️  Platform:  %s\n", platformLabel())
	fmt.Printf("📦 Version:   onnxruntime v%s\n", ortVersion)
	fmt.Printf("🔗 URL:       %s\n", downloadURL)
	fmt.Printf("📁 Dest:      %s\n\n", destPath)

	// Download archive
	fmt.Printf("⬇️  Downloading %s…\n", archiveName)
	data, err := downloadBytes(downloadURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	fmt.Printf("   📦 %.1f MB downloaded\n", float64(len(data))/(1024*1024))

	// Extract shared lib from .tgz
	fmt.Printf("📂 Extracting %s…\n", libName)
	libData, err := extractLibFromTGZ(data, libName)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := os.WriteFile(destPath, libData, 0o755); err != nil {
		return fmt.Errorf("write lib: %w", err)
	}

	fmt.Printf("✅ Installed: %s (%.1f MB)\n\n", destPath, float64(len(libData))/(1024*1024))
	printNextSteps(destPath)
	return nil
}

func printNextSteps(libPath string) {
	fmt.Println("── Next steps ────────────────────────────────────────────")
	fmt.Println("1. Download an embedding model:")
	fmt.Println("     axon model download bge-small-zh-v1.5")
	fmt.Println()
	fmt.Println("2. Rebuild axon with ONNX support:")
	fmt.Println("     CGO_ENABLED=1 go build --tags \"fts5 onnx\" -o axon .")
	fmt.Println()
	fmt.Println("3. Set the default model:")
	fmt.Println("     export AXON_DEFAULT_MODEL=bge-small-zh-v1.5")
	fmt.Println()
	fmt.Printf("   (Optional) Override lib path:\n")
	fmt.Printf("     export AXON_ORT_LIB=%s\n", libPath)
	fmt.Println("──────────────────────────────────────────────────────────")
}

// ── platform detection ────────────────────────────────────────────────────────

func ortAssetInfo() (libName, archiveName, url string, err error) {
	// Use runtime detection at build time for the CLI
	// This runs at runtime on the user's actual machine.
	goos, goarch := currentPlatform()

	var platform string
	switch goos {
	case "darwin":
		switch goarch {
		case "arm64":
			platform = "osx-arm64"
			libName = "libonnxruntime." + ortVersion + ".dylib"
		case "amd64":
			platform = "osx-x86_64"
			libName = "libonnxruntime." + ortVersion + ".dylib"
		default:
			err = fmt.Errorf("unsupported macOS arch: %s", goarch)
			return
		}
	case "linux":
		switch goarch {
		case "amd64":
			platform = "linux-x64"
			libName = "libonnxruntime.so." + ortVersion
		case "arm64":
			platform = "linux-aarch64"
			libName = "libonnxruntime.so." + ortVersion
		default:
			err = fmt.Errorf("unsupported Linux arch: %s", goarch)
			return
		}
	case "windows":
		if goarch == "amd64" {
			platform = "win-x64"
			libName = "onnxruntime.dll"
		} else {
			err = fmt.Errorf("unsupported Windows arch: %s", goarch)
			return
		}
	default:
		err = fmt.Errorf("unsupported OS: %s", goos)
		return
	}

	archiveName = fmt.Sprintf("onnxruntime-%s-%s.tgz", platform, ortVersion)
	url = fmt.Sprintf(
		"https://github.com/microsoft/onnxruntime/releases/download/v%s/%s",
		ortVersion, archiveName,
	)
	return
}

func platformLabel() string {
	goos, goarch := currentPlatform()
	return fmt.Sprintf("%s/%s", goos, goarch)
}

// currentPlatform returns the OS and arch at runtime.
// Separate function so it can be easily tested / mocked.
func currentPlatform() (goos, goarch string) {
	// We use build-time constants via runtime package in actual build;
	// here we call os.Getenv overrides first for testing.
	if v := os.Getenv("AXON_TEST_GOOS"); v != "" {
		goos = v
	} else {
		goos = runtimeGOOS()
	}
	if v := os.Getenv("AXON_TEST_GOARCH"); v != "" {
		goarch = v
	} else {
		goarch = runtimeGOARCH()
	}
	return
}

// ── download helpers ──────────────────────────────────────────────────────────

func downloadBytes(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// extractLibFromTGZ extracts the named shared library from a .tgz archive.
// The lib may be nested under a directory (e.g. onnxruntime-osx-arm64-1.21.0/lib/libonnxruntime.dylib).
func extractLibFromTGZ(data []byte, libName string) ([]byte, error) {
	gr, err := gzip.NewReader(newBytesReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip open: %w", err)
	}
	defer gr.Close()
	return readLibFromTar(tar.NewReader(gr), libName)
}

func readLibFromTar(tr *tar.Reader, libName string) ([]byte, error) {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		// Match by filename (basename) — handles nested paths
		base := filepath.Base(hdr.Name)
		// Handle versioned symlinks: libonnxruntime.1.21.0.dylib vs libonnxruntime.dylib
		if base == libName || strings.HasPrefix(base, strings.Split(libName, ".")[0]) && strings.Contains(base, ortVersion) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("library %q not found in archive", libName)
}

type bytesReaderWrapper struct {
	data   []byte
	offset int
}

func newBytesReader(data []byte) io.Reader {
	return &bytesReaderWrapper{data: data}
}

func (r *bytesReaderWrapper) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}
