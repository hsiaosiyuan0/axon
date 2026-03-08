# Installation

## Download binary (recommended)

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-darwin-arm64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon

# macOS (Intel)
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-darwin-amd64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon

# Linux (amd64)
curl -fsSL https://github.com/hsiaosiyuan0/axon/releases/latest/download/axon-linux-amd64 \
  -o /usr/local/bin/axon && chmod +x /usr/local/bin/axon
```

## Build from source

**Requirements:** Go 1.22+, GCC (for CGO/SQLite)

```bash
git clone https://github.com/hsiaosiyuan0/axon
cd axon
make build       # produces ./axon
make install     # copies to /usr/local/bin/axon
```

## Windows

Windows users are recommended to use [WSL](https://learn.microsoft.com/en-us/windows/wsl/) and follow the Linux installation steps above.
