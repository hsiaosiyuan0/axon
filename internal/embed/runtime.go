//go:build onnx
// +build onnx

package embed

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	ort "github.com/yalue/onnxruntime_go"
)

// OrtVersion is the onnxruntime shared library version this build targets.
const OrtVersion = "1.21.0"

// ortAssets embeds the platform-specific libonnxruntime at build time.
// The build script (scripts/build.sh --onnx) is responsible for placing the
// correct .tgz archive into assets/ before `go build` is invoked.
//
//go:embed assets
var ortAssets embed.FS

// ortLibName returns the platform-specific shared library filename.
func ortLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime." + OrtVersion + ".dylib"
	case "linux":
		return "libonnxruntime.so." + OrtVersion
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so." + OrtVersion
	}
}

// ortArchiveName returns the embed asset filename (inside assets/).
func ortArchiveName() string {
	var platform string
	switch runtime.GOOS {
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			platform = "osx-arm64"
		case "amd64":
			platform = "osx-x86_64"
		default:
			platform = "osx-x86_64"
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			platform = "linux-x64"
		case "arm64":
			platform = "linux-aarch64"
		default:
			platform = "linux-x64"
		}
	case "windows":
		platform = "win-x64"
	default:
		platform = "linux-x64"
	}
	return fmt.Sprintf("onnxruntime-%s-%s.tgz", platform, OrtVersion)
}

// OrtLibURL returns the GitHub release download URL for this platform/arch.
// Still exported so scripts can use it for downloading.
func OrtLibURL() (url, filename string, err error) {
	archiveName := ortArchiveName()
	url = fmt.Sprintf(
		"https://github.com/microsoft/onnxruntime/releases/download/v%s/%s",
		OrtVersion, archiveName,
	)
	return url, ortLibName(), nil
}

// DefaultLibDir returns the directory where axon stores the ort shared library.
func DefaultLibDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".axon", "lib")
}

// extractEmbeddedLib extracts the embedded libonnxruntime archive to destDir.
func extractEmbeddedLib(destDir string) (string, error) {
	archiveName := ortArchiveName()
	assetPath := "assets/" + archiveName

	data, err := ortAssets.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("embedded asset not found (%s): %w\n"+
			"  → run `scripts/build.sh --onnx` to rebuild with assets", assetPath, err)
	}

	libName := ortLibName()
	destPath := filepath.Join(destDir, libName)

	// Already extracted?
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create lib dir: %w", err)
	}

	// Use bytes reader
	br := &bytesReader{data: data}
	gzr, err := gzip.NewReader(br)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}
		// The library lives at e.g.:
		//   onnxruntime-osx-arm64-1.21.0/lib/libonnxruntime.1.21.0.dylib
		//   onnxruntime-linux-x64-1.21.0/lib/libonnxruntime.so.1.21.0
		//   onnxruntime-win-x64-1.21.0/lib/onnxruntime.dll
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == libName {
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return "", fmt.Errorf("create lib file: %w", err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", fmt.Errorf("write lib file: %w", err)
			}
			f.Close()
			return destPath, nil
		}
	}

	return "", fmt.Errorf("library %s not found in embedded archive %s", libName, archiveName)
}

// bytesReader implements io.Reader over a []byte.
type bytesReader struct {
	data []byte
	pos  int
}

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}

// FindOrtLib searches for the shared library in:
//  1. AXON_ORT_LIB env var (explicit path)
//  2. ~/.axon/lib/<libname>  (previously extracted)
//  3. Embedded asset → extract to ~/.axon/lib/
//  4. System library paths
func FindOrtLib() (string, error) {
	// 1. Explicit env override
	if p := os.Getenv("AXON_ORT_LIB"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("AXON_ORT_LIB=%s not found", p)
	}

	libDir := DefaultLibDir()
	libName := ortLibName()

	// 2. Already extracted to ~/.axon/lib/
	candidate := filepath.Join(libDir, libName)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	// 3. Extract from embedded asset
	extracted, err := extractEmbeddedLib(libDir)
	if err == nil {
		return extracted, nil
	}

	// 4. System paths (fallback)
	for _, dir := range systemLibPaths() {
		p := filepath.Join(dir, libName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf(
		"libonnxruntime not found and no embedded asset available\n"+
			"  → rebuild with `scripts/build.sh --onnx`\n"+
			"  → or set AXON_ORT_LIB=/path/to/%s", libName,
	)
}

func systemLibPaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/usr/local/lib", "/opt/homebrew/lib", "/usr/lib"}
	case "linux":
		return []string{"/usr/local/lib", "/usr/lib", "/usr/lib/x86_64-linux-gnu"}
	default:
		return nil
	}
}

// InitOrtOnce initializes the onnxruntime environment exactly once.
// It auto-discovers (or extracts from embed) the shared library.
func InitOrtOnce() error {
	if ort.IsInitialized() {
		return nil
	}

	libPath, err := FindOrtLib()
	if err != nil {
		return err
	}

	ort.SetSharedLibraryPath(libPath)

	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("init onnxruntime (lib=%s): %w", libPath, err)
	}
	return nil
}
