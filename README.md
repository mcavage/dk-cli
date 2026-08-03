# dk

`dk` is an agent-first CLI for DigiKey: an AI agent builds a parts list, `dk bom price` renders a terminal table you read, and `dk bom push` hands you one URL that loads a DigiKey cart so you can click buy.

Disclaimer: `dk` is an independent open-source tool and is not affiliated with, endorsed by, or sponsored by DigiKey.

```bash
dk part search "10k 0805"
dk bom price bom.csv --table
dk bom push bom.csv --open
```

`dk` outputs JSON on stdout by default, renders `--table` for human review, outputs structured error envelopes, and exits non-zero on failure.

Set `DK_CLIENT_ID` and `DK_CLIENT_SECRET` for authentication. Any value starting with `op://` is resolved automatically from 1Password using `op read`.

Run `dk schema` to output machine-readable CLI interface documentation, flag definitions, and example payloads.

`dk` cannot place orders. The binary contains no ordering client and holds product-data credentials only.

## Installation

### From source (works today)

```
git clone https://github.com/mcavage/dk-cli && cd dk-cli && make install
```

### Released binaries

Everything below needs a tagged release to exist first. Until `v0.1.0` is
pushed, these commands will fail, and that is stated here rather than
discovered:

```
go install github.com/mcavage/dk-cli/cmd/dk@latest      # needs the repo pushed
brew install mcavage/tap/dk                             # needs the tap created
curl -fsSL https://raw.githubusercontent.com/mcavage/dk-cli/main/install.sh | sh
```

`install.sh` downloads the right binary for your OS and architecture into
`~/.local/bin`, verifies its SHA256 against the release checksums, and refuses
to install anything it cannot verify. It needs no root.

Release machinery is in the repo and ready: `make dist` cross-compiles all four
platforms with checksums, `.github/workflows/release.yml` publishes them on a
`v*` tag, and `scripts/update-tap.sh` regenerates the Homebrew formula pinned to
those checksums. The tap itself is a separate `homebrew-tap` repo that has to be
created once, with a `HOMEBREW_TAP_TOKEN` secret; until then the release job
skips that step rather than failing.

## DigiKey Credentials

1. Go to [developer.digikey.com](https://developer.digikey.com) and create an account.
2. Create an App to obtain a Client ID and Client Secret.
3. Subscribe your App to **Product Information V4**.

App registration alone is insufficient. You must explicitly subscribe your app to Product Information V4, or requests return HTTP 401 Unauthorized (`NOT_SUBSCRIBED`).

Set credentials in your environment:
```bash
export DK_CLIENT_ID="your_client_id"
export DK_CLIENT_SECRET="your_client_secret"
```

To resolve secrets dynamically from 1Password:
```bash
export DK_CLIENT_ID="op://vault/digikey/client_id"
export DK_CLIENT_SECRET="op://vault/digikey/client_secret"
```

## BOM Input Formats

`dk` accepts parts lists in three formats:

### 1. Canonical CSV
Uses standard headers `mpn,qty,refdes` with an optional `manufacturer`:
```csv
mpn,qty,refdes,manufacturer
RC0805FR-0710KL,10,"R1,R2",Yageo
1050-ABX00052-ND,1,"U1",Arduino
```

### 2. Markdown Pipe Tables
Requires `MPN` and `Qty` columns:
| MPN | Qty | RefDes | Manufacturer |
|---|---|---|---|
| RC0805FR-0710KL | 10 | R1, R2 | Yageo |
| 1050-ABX00052-ND | 1 | U1 | Arduino |

### 3. KiCad CSV Exports
Remap arbitrary export headers using `--columns`:
```bash
dk bom price bom.csv --columns mpn=MPN,qty=Qty,refdes=Reference
```

### Buy vs Qty Column Rule
BOM inputs state `need` quantity. `dk` resolves packaging variations (cut tape, tape and reel, DigiReel) and enforces Minimum Order Quantity (MOQ) constraints:
- `need`: target units specified in your BOM.
- `order_qty`: actual units required after applying variation MOQ and package rules.
- `overbuy_units`: `order_qty - need`.
- `overbuy_cost`: total cost of extra required units (`overbuy_units * unit_price + flat_fees`).

Terminal tables and JSON envelopes call out overbuy costs separately so you can review excess spend before pushing to a cart.

## Design Notes

- **Go stdlib static binary**: Built with standard library Go only. Zero external dependencies. Cold start under 10ms.
- **Packaging variation policy**: Selects the packaging option yielding the lowest landed total cost after MOQ forcing. Handles `StandardPackage == 0` without division errors. Includes flat fees (such as $7 DigiReel charges) in landed cost comparisons so lower-MOQ reels with fees are rejected when cut tape costs less.
- **10-minute token lifecycle**: DigiKey OAuth2 tokens expire in 10 minutes (599 seconds). `dk` caches tokens in `$XDG_STATE_HOME/dk/token.json` (0600 permissions) with file locking to prevent stampedes across concurrent CLI invocations.
- **Gated cart handoff**: `dk bom push` validates BOM reports before generating handoff URLs. It hard-refuses to push if any line is unmatched, not orderable, blocked (EOL/Discontinued/last buy chance), or exceeds overbuy limits. Cart URLs are single-use and expire in minutes, so `dk bom push` opens your browser immediately by default.
- **24-hour search staleness**: `KeywordSearch` data can be up to 24 hours stale upstream. `dk` uses `ProductDetails` for real-time pricing, stock, MOQ, and lifecycle status.
