### 1. Naming and Trademark

Recommendation: Name the repository and binary `mouser-cli`.

Reasoning and PyPI Collision: The name `mouser` is already taken on PyPI by `sparkmicro/mouser-api`, which installs an executable named `mouser`. Using `mouser` creates binary shadowing, package manager collisions, and command ambiguity.

Trademark Risk: Using `mouser` alone implies an official product from Mouser Electronics, Inc. Using `mouser-cli` clearly identifies it as a third-party interface tool.

Tradeoff: Giving up the shorter `mouser` binary name in exchange for eliminating package collisions and trademark ambiguity.

Mandatory Disclaimer Line:
mouser-cli is an independent open-source tool and is not affiliated with, endorsed by, or sponsored by Mouser Electronics, Inc.

### 2. Distribution Paths

One command per audience:

* pix user via pack: `pix pack add mouser`
* Mac human via Homebrew: `brew install username/tap/mouser-cli`
* Linux or CI user: `curl -fsSL https://raw.githubusercontent.com/username/mouser-cli/main/install.sh | sh`
* Agent in another harness (Claude Code, Codex, Cursor): `go install github.com/username/mouser-cli@latest`

Tradeoff: Requiring Go toolchain or single-binary download for non-pix agents instead of an npm package gives zero-dependency native binaries without Node runtime overhead.

### 3. pix Pack Contents and Setup

A pix pack for this tool must contain:
* Binary entrypoint or wrapper script exposed to PATH.
* Skill definition file teaching agents catalog lookup, cart management, and order safety workflows.
* Knowledge file covering API rules (200 OK error responses, full cart replacement behavior, minimum order quantities).
* Pack manifest declaring binary locations and healthcheck hooks (`EXTRA_CLIS`).
* Host-side credential setup guidance using `sbx secret set` on the host machine.

Pack Layout:
* `packs/mouser/pack.json` (Pack manifest)
* `packs/mouser/bin/mouser-cli` (CLI binary)
* `packs/mouser/skills/mouser/SKILL.md` (Agent skill file)
* `packs/mouser/knowledge/mouser-api.md` (API gotchas)

Setup UX:
1. User installs pack on host via `pix pack add mouser`.
2. Host prompts user to set API keys using `sbx secret set mouser_search_key` and `sbx secret set mouser_order_key`.
3. Sandbox injects secrets into VM environment variables (`MOUSER_SEARCH_API_KEY`, `MOUSER_ORDER_API_KEY`).

Host Verification Flag:
Must verify against host pix implementation: exact pack manifest schema (`pack.json` vs `pack.toml`), automatic PATH symlinking behavior for pack binaries, and secret-to-environment-variable mapping conventions.

### 4. README and AGENTS.md Structure (First 30 Lines)

First 30 lines requirements:
1. Title and 1-line mission: Machine-readable CLI for Mouser Electronics catalog search, carts, and order execution.
2. Required disclaimer: Non-affiliation notice for Mouser Electronics, Inc.
3. Primary usage pattern: Direct command example (`mouser-cli search --keyword "STM32F407" --json`).
4. Output guarantees: Pure JSON to stdout, structured JSON errors to stderr, non-zero exit code on failure.
5. Auth requirements: `MOUSER_SEARCH_API_KEY` and `MOUSER_ORDER_API_KEY` environment variables.
6. Schema discovery: `mouser-cli --help` and `mouser-cli schema`.
7. Safety gate notice: Destructive cart actions require `--force` and order execution requires `--confirm-order`.

### 5. MCP Shim Evaluation

Recommendation: No MCP shim.

Argument: Exposing an MCP shim adds background process management, protocol translation overhead, and duplicate maintenance surfaces. Agents in pix, Claude Code, and Cursor natively execute CLI commands in terminal contexts. A clean CLI that returns deterministic JSON and structured schemas gives agents lower token usage and direct shell composability without background server lifecycles.

Tradeoff: Giving up compatibility with GUI-only MCP clients (like Claude Desktop) in exchange for zero daemon management, lower token footprint, and universal shell execution across all CLI-capable agents.
