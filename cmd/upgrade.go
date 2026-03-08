package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X github.com/hsiaosiyuan0/axon/cmd.Version=v1.2.3"
var Version = "dev"

const (
	upgradeOwner = "hsiaosiyuan0"
	upgradeRepo  = "axon"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Check for a newer version of Axon",
	Long: `Check GitHub for the latest Axon release and display upgrade instructions.

The current binary version is compared to the latest GitHub release.
If a newer version is available, download instructions are printed.

To set the current version at build time:
  go build -ldflags "-X github.com/hsiaosiyuan0/axon/cmd.Version=v1.2.3" -o axon .`,
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")

		if !quiet {
			fmt.Printf("🦞 Axon upgrade check\n")
			fmt.Printf("   Current version : %s\n", Version)
			fmt.Printf("   Checking GitHub  : https://github.com/%s/%s/releases\n\n",
				upgradeOwner, upgradeRepo)
		}

		latest, err := fetchLatestRelease()
		if err != nil {
			return fmt.Errorf("check latest release: %w", err)
		}

		if !quiet {
			fmt.Printf("   Latest version  : %s (%s)\n", latest.TagName, latest.PublishedAt[:10])
			fmt.Printf("   Release notes   : %s\n\n", latest.HTMLURL)
		}

		if latest.TagName == Version {
			fmt.Println("✅ You are running the latest version.")
			return nil
		}

		if Version == "dev" {
			fmt.Printf("ℹ️  Development build — latest release is %s\n", latest.TagName)
			printInstallInstructions(latest.TagName)
			return nil
		}

		// Compare versions (simple string comparison works for semver vX.Y.Z)
		if latest.TagName > Version {
			fmt.Printf("🆕 New version available: %s → %s\n\n", Version, latest.TagName)
			printInstallInstructions(latest.TagName)
		} else {
			fmt.Println("✅ You are running the latest version.")
		}

		return nil
	},
}

// githubRelease is the minimal subset of the GitHub release API response.
type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int    `json:"size"`
	} `json:"assets"`
}

func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest",
		upgradeOwner, upgradeRepo)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("axon-upgrade/%s", Version))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("no releases found (repository may not have any releases yet)")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &release, nil
}

func printInstallInstructions(tag string) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map Go arch names to release asset names
	archMap := map[string]string{
		"amd64": "amd64",
		"arm64": "arm64",
		"386":   "386",
	}
	releaseArch := archMap[goarch]
	if releaseArch == "" {
		releaseArch = goarch
	}

	var assetName string
	switch goos {
	case "windows":
		assetName = fmt.Sprintf("axon_%s_windows_%s.exe", tag, releaseArch)
	case "darwin":
		assetName = fmt.Sprintf("axon_%s_darwin_%s", tag, releaseArch)
	default:
		assetName = fmt.Sprintf("axon_%s_linux_%s", tag, releaseArch)
	}

	baseURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s",
		upgradeOwner, upgradeRepo, tag)
	downloadURL := fmt.Sprintf("%s/%s", baseURL, assetName)

	fmt.Println("📦 Install instructions:")
	fmt.Println()

	switch goos {
	case "windows":
		fmt.Printf("   Download: %s\n", downloadURL)
		fmt.Printf("   Then replace your axon.exe with the downloaded file.\n")
	case "darwin", "linux":
		fmt.Printf("   # Quick install (replace /usr/local/bin/axon with your install path):\n")
		fmt.Printf("   curl -fsSL %s -o /tmp/axon-new \\\n", downloadURL)
		fmt.Printf("     && chmod +x /tmp/axon-new \\\n")
		fmt.Printf("     && sudo mv /tmp/axon-new $(which axon)\n")
		fmt.Println()
		fmt.Printf("   # Or download manually:\n")
		fmt.Printf("   %s\n", downloadURL)
	}

	fmt.Println()
	fmt.Printf("   SHA256 checksums: %s/SHA256SUMS.txt\n", baseURL)
	fmt.Println()

	// Print abbreviated release notes (first 300 chars)
	_ = tag // used above
	fmt.Println("   📝 Tip: run 'axon upgrade' after updating to confirm the new version.")

	// If AXON_PRINT_ENV is set, show current env for debugging
	if os.Getenv("AXON_DEBUG") != "" {
		fmt.Printf("\n   [debug] GOOS=%s GOARCH=%s asset=%s\n", goos, goarch, assetName)
	}
}

// printReleaseNotes prints the first few lines of release notes.
func printReleaseNotes(body string) {
	if body == "" {
		return
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	maxLines := 5
	if len(lines) < maxLines {
		maxLines = len(lines)
	}
	fmt.Println("   Release notes preview:")
	for _, l := range lines[:maxLines] {
		fmt.Printf("   %s\n", l)
	}
	if len(lines) > maxLines {
		fmt.Printf("   ... (%d more lines)\n", len(lines)-maxLines)
	}
}

func init() {
	upgradeCmd.Flags().BoolP("quiet", "q", false, "Only print version comparison result")
}
