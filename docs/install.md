# Install mcpx

Windows users: use WSL2 and run these commands inside your Linux distro shell.

## Homebrew (Tap Cask)

```bash
brew tap lydakis/mcpx
brew install --cask mcpx
```

Update:

```bash
brew upgrade --cask mcpx
```

## npm Wrapper

Installs the `mcpx-go` wrapper package, which provides a `mcpx` command that downloads the matching `mcpx` binary from GitHub releases.

```bash
npm install -g mcpx-go
mcpx --version
```

## PyPI Wrapper

Installs the `mcpx-go` wrapper package, which provides a `mcpx` command that downloads the matching `mcpx` binary into your cache.

```bash
pip install mcpx-go
mcpx --version
```

## Manual Install

1. Download the archive for your platform from the release assets.
2. Extract the archive.
3. Move `mcpx` into your `PATH`.
4. Install `mcpx.1` into your manpath.

Example (macOS arm64):

```bash
curl -L -o mcpx.tar.gz https://github.com/lydakis/mcpx/releases/download/v0.1.0/mcpx_0.1.0_darwin_arm64.tar.gz
tar -xzf mcpx.tar.gz
chmod +x mcpx
mv mcpx /usr/local/bin/mcpx
mkdir -p "${XDG_DATA_HOME:-$HOME/.local/share}/man/man1"
cp man/man1/mcpx.1 "${XDG_DATA_HOME:-$HOME/.local/share}/man/man1/"
```

Verify:

```bash
mcpx --version
man mcpx
```

## Shell Completion Install

```bash
mcpx completion bash > ~/.local/share/bash-completion/completions/mcpx
mcpx completion zsh > "${fpath[1]}/_mcpx"
mcpx completion fish > ~/.config/fish/completions/mcpx.fish
```

## Install Built-In Agent Skill

```bash
mcpx skill install
```

This installs the built-in `mcpx` skill at `~/.agents/skills/mcpx` and creates a Claude Code symlink at `~/.claude/skills/mcpx`.

Optional links:

```bash
mcpx skill install --codex-link
mcpx skill install --kiro-link
mcpx skill install --codex-dir /custom/.codex/skills --kiro-dir /custom/.kiro/skills
```
