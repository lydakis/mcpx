# Install mcpx

## Homebrew (Tap Cask)

```bash
brew tap lydakis/mcpx
brew install --cask mcpx
```

Update:

```bash
brew upgrade --cask mcpx
```

## Manual Install

1. Download the archive for your platform from the release assets.
2. Extract the archive.
3. Move `mcpx` into your `PATH`.

Example (macOS arm64):

```bash
curl -L -o mcpx.tar.gz https://github.com/lydakis/mcpx/releases/download/v0.1.0/mcpx_0.1.0_darwin_arm64.tar.gz
tar -xzf mcpx.tar.gz
chmod +x mcpx
mv mcpx /usr/local/bin/mcpx
```

Verify:

```bash
mcpx --version
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
