# mouser-cli: plan

Agent-first CLI for the Mouser Electronics API. Not an MCP server.
Status: plan only, no code written.

**The product in one line:** BOM file in, priced sourcing report out, a real Mouser cart
built, and a link the human opens to click Buy. The CLI never submits an order.

## 0. Decided

- **No autonomous ordering.** `order submit` is cut permanently, not deferred. The binary
  will contain no code path that can set `SubmitOrder=true`. See D9.
- **Binary is `mouser`.** Repo is `mouser-cli`. See D10 for the PyPI collision handling.
- **Keys are not provisioned yet.** Build order is offline-first. See section 10.

## 1. Prior art (assessed, not guessed)

| Project | What it is | Verdict |
|---|---|---|
| `nickweedon/mouser-mcp-docker` | Python/FastMCP, 9 tools, Docker, `.env` | The reference. Its own `API_REVIEW_FINDINGS.md` admits it shipped with header auth (wrong, it is a query param), wrong cart paths, and wrong order verb. Implements ~11 of 26 endpoints. |
| `cmaurer/mouser-search-mcp` | Search only | Narrow. |
| `sparkmicro/mouser-api` (pypi `mouser`) | Python lib + small CLI, powers Ki-nTree | Closest existing CLI. Owns the binary name `mouser` on PyPI. Human-shaped output. |
| `PatrickWalther/go-mouser` | Go client, claims 100% endpoint coverage, stdlib only | Best client to imitate. Library, not a CLI. |

Design canon used: clispec.dev, `Johnixr/agent-cli-guide`, arcjet "Designing a CLI for AI agents",
open-cli-collective output-and-rendering.

## 2. The API, verified from live swagger

Source: `https://api.mouser.com/api/docs/V1` and `/V2` (fetched, not recalled).

- Auth is `?apiKey=<uuid>` as a **query param**, and there are **two separate keys**:
  Search, and Order/Cart. The order key is bound to one Mouser account.
- 23 v1 endpoints + 3 v2. The v2 trio (`keywordandmanufacturer`,
  `partnumberandmanufacturer`, `manufacturerlist`) deprecates the v1 versions.
- Limits: 50 results per search, ~30 req/min, 1000 req/day. **No sandbox environment.**
- Responses are PascalCase and frequently return **HTTP 200 with a populated `Errors[]`**.
- `POST /cart` is a **full replace**: any part number omitted is deleted.
- `POST /order` with `SubmitOrder=true` **places and charges a real order**. We never call it.
- `MouserPart` has ~40 fields. Raw passthrough is the token disaster the MCPs suffer from.

Three things learned from `SergeoLacruz/inventree-supplier-panel` (a plugin doing this against
the real API), which the swagger does not tell you:
- **Sending an empty `CartKey` creates a new cart.** There is no create-cart endpoint.
- **`CartItems[]` entries carry their own `Errors[]`.** A cart insert can return top-level
  success with individual lines failed. Any client that only checks the top-level array will
  report a cart it did not actually build. This is a per-line error model.
- Cart line fields not in the swagger's own docs: `MouserATS` (available to ship),
  `CartItemCustPartNumber`, `PackagingChoice`, `UnitPrice`, `ExtendedPrice`.
- Its README states carts it creates appear in the supplier's web account and must be deleted
  through the web interface. That is real-world evidence for the handoff model in D9, though
  not proof of a deep link. See Q1.

## 3. Decisions

### D1. Go, single static binary
Stdlib only covers this API. ~5-10ms cold start matters when an agent invokes it 40 times
in a session. Cross-compiles, installs unprivileged, no runtime.
Rejected: Node/npx (200-600ms boot per call), Python/uv (same plus interpreter assumptions),
Rust (buys nothing here, costs a solo maintainer).

### D2. Vendored swagger, committed generated DTOs, hand-written view layer
`api/spec/V1.json` and `V2.json` committed. `go generate` produces DTOs, output is committed
so it is reviewable. Never fetch the spec at build time. A nightly CI job diffs live swagger
against the vendored copy and opens an issue on drift. Decoders tolerate unknown fields; raw
bytes are retained so `--format raw` never loses a field Mouser added yesterday.

### D3. Two-layer output, ~12-field default view
Default search record: `mouser_pn`, `mpn`, `manufacturer`, `description` (truncated),
`stock` (int, parsed), `min`, `mult`, `lead_time_days`, `unit_price` + `currency` at the
requested qty, `lifecycle` (normalized), `rohs`, `datasheet_url`, `product_url`.
Roughly 150 tokens per record instead of ~900. Escape hatches: `--fields`, `--price-breaks`,
`--full`, `--format raw`. Parse failures emit `null` plus `*_raw`, never a guess.

### D4. JSON always, no TTY sniffing
Format is a function of explicit input only: JSON by default for everyone, `--human` for
tables, `MOUSER_OUTPUT=human` as the env override. When stdout is a TTY and no format was
chosen, print JSON and put `tip: add --human` on **stderr**. TTY detection is the top source
of "worked in my shell, broke in the agent".

One envelope for every command:
```
{"ok":true,"command":"part.search","data":{...},"warnings":[],"meta":{...}}
{"ok":false,"command":"cart.get","error":{"code":"CART_KEY_MISSING","message":"...","retryable":false,"fix":"<runnable command>","details":{"upstream":[...]}}}
```
`error.code` is a stable enum. `error.fix` is a literal runnable command or null. Mouser's
`Errors[]` on a 200 is mapped at the transport layer to `ok:false` plus a non-zero exit.

Exit codes: 0 ok, 2 usage, 3 credential, 4 not-found/ambiguous, 5 upstream rejection,
6 rate limit, 7 network, 8 destructive-refused, 9 partial (data usable), 1 internal.
The rule agents get taught: **0 or 9 means `data` is usable, anything else means read `error.fix`.**

### D5. Discoverability is two artifacts, one source of truth
`mouser schema` emits the whole command surface as one JSON doc (commands, flags, types,
enums, destructive markers, exit table, envelope, one example each). `--compact` drops prose.
Plus a shipped `AGENTS.md` (also `mouser agents-md`) carrying the rules schema cannot express.
Human `--help` renders from the same schema data so they cannot drift.
Rejected: per-subcommand `--help --json` (N round trips is the token burn we are avoiding).

### D6. Never truncate silently
Every list response carries `meta.page = {returned, total_upstream, page, page_size, has_more,
next_command}` where `next_command` is a literal executable string. Truncation also pushes a
warning code. Field projection is truncation too: `meta.fields = {mode:"summary", omitted:32,
full:"--fields all"}`. The hard 50 cap gets its own code (`UPSTREAM_CAP_50`) distinct from
ordinary pagination, so an agent can tell "fetch more" from "refine your query".

### D7. Cache + cross-process rate ledger
XDG paths: config in `$XDG_CONFIG_HOME/mouser`, responses in `$XDG_CACHE_HOME/mouser`, rate
ledger and cart keys in `$XDG_STATE_HOME/mouser` (0600). Cache key is
`sha256(cli_version + endpoint + normalized_body + key_fingerprint)`.
TTL by data class: search/part 24h, manufacturers/currencies/countries 30d, cart/order/history
never. Any cart mutation purges cart entries. `--refresh`, `--no-cache`.
Rate limiting must be **cross-process** (agents run concurrent invocations): one
`ratelimit.json` under `flock`, sliding 60s window plus a UTC-day counter, enforced
conservatively at 25/min and 900/day so a human on the website is not locked out by the agent.
Exhaustion exits non-zero with `retry_after_seconds`; `--wait` opts into blocking.
Every response reports `meta.cache = {hit, age_s, ttl_s, stale}`. Price data refuses to serve
stale under rate limit rather than quoting an old price.

### D8. No test environment, so record/replay
`MOUSER_CASSETTE_DIR` records real responses once; the suite runs offline against cassettes.
This is the only honest way to test a money-spending API with no sandbox.

## 4. Credentials

Resolution chain, first hit wins, source name (never value) printed on `--verbose`:
1. `MOUSER_SEARCH_API_KEY` / `MOUSER_ORDER_API_KEY`. **If the value starts with `op://`,
   resolve it via `op read`.** This is the bridge that makes 1Password primary without
   hardcoding it and without requiring `op` where it does not exist.
2. `op read` of secret references declared in config (`op://Private/Mouser Search/credential`).
3. OS keychain (`security` / `secret-tool`).
4. Plaintext in `config.toml`, accepted only at mode 0600, warn otherwise.

`op read` beats the alternatives: `op run` is the wrong shape for a CLI resolving its own
config, `op item get` returns more than needed, and a service-account token is itself a
credential at rest that grants vault-wide read.

Hard prohibitions:
- **No `--api-key` flag.** Flags land in shell history, `ps`, and agent transcripts.
- **Never emit a request URL** anywhere. The key is in the query string, so a URL is a credential.
- Never write a key to cache, lockfile, or temp file. Cache by endpoint+body hash, never by URL.

Redaction is a single sink, not per call site: `s/([?&]apiKey=)[^&\s]+/${1}REDACTED/gi` plus an
exact-match scrub of both resolved values against every emitted line. HTTP errors report method
and path only.

### pix wiring
Host-side `op read` at provision time, injected with `sbx secret set`, surfaced in-VM as the two
env vars. The CLI in-VM is unmodified and just reads env. This matches the existing house
pattern (model keys, github token) and needs no new infrastructure.
Rejected: installing `op` in-VM (no sudo/apt, and it would put 1Password session material inside
the agent's blast radius). Noted as upgrade path: a host-side capability proxy that injects the
key per request so the VM never holds it, same shape as gog/Slack. Not worth it for two keys
and one user at v1.
Residual risk, accepted: the keys are in the sandbox env, so agent-run code can read them.
Mitigated by egress policy (allow `api.mouser.com`), single-account blast radius, and rotation.

## 5. Order safety: structural, not procedural

### D9. The CLI cannot spend money, by construction

The confirm-token design from the first draft is deleted along with the feature it guarded.
Guardrails you can argue with are worse than a capability you do not have.

- There is **no `order submit` command**, and `SubmitOrder` is not a settable field anywhere in
  the codebase. The order-create request type hardcodes it to `false` at the type level, so
  there is no flag, env var, or config key that turns it on. A CI grep asserts the string
  `SubmitOrder` appears in exactly one place set to exactly one value.
- `POST /order` is therefore called **only** in its dry-run form, and it is not exposed as an
  order command at all. It is exposed as **`mouser cart quote`**, because that is what it does
  for us: shipping options, tax, and the landed total for a cart, without buying anything.
- The terminal command is **`mouser cart open`**: it prints the cart key, the line-by-line
  contents, the merchandise total, and the URL the human opens to review and click Buy.
  `--open` launches the browser. That is the handoff.
- `cart replace` (the destructive full-replace `POST /cart`) still needs `--confirm-replace`
  and still prints the exact deletion list first. Agents are pointed at `cart add` / `cart
  update`. This is the only remaining destructive path, and the worst it can do is lose a cart.
- Retries are now safe to be generous with, because no call is a purchase. Cart inserts are
  still not blindly retried: a timed-out insert followed by a retry duplicates lines, so a
  retry re-reads the cart first and reconciles against intent.

What this gives up: the fully autonomous "agent buys the parts" demo. What it buys is that the
worst possible outcome of a hallucinating agent, a bad BOM parse, or a wrong `Mult` is a cart
you look at and reject, against an API with no test environment. Reviewing a wrong cart costs
30 seconds. Every remaining failure mode is recoverable by closing a browser tab.

## 6. Command surface

Global: JSON default, long flags only, never interactive, `--dry-run` on every mutating command.

**part** (search key only): `search <keyword>` (`--mfr` routes to v2), `get <mpn>`,
`price <mpn> --qty N`, `alternates <mpn>` (client-side heuristic, no such endpoint),
`mfr list`.

**bom** (search key only, composite): `price <file>`, `resolve <file> -o bom.lock`,
`to-cart <file>` (the money path, minus the money).

**cart** (order key): `show`, `add`, `update`, `remove`, `check`, `quote`, **`open`**,
`replace --confirm-replace`, `schedule set|update|clear`.

**order** (order key, read-only): `get`, `history`, `reorder --to-cart`, `track`.
There is no `order submit`. `order reorder` lands in a cart, never in an order.

**meta**: `auth status`, `doctor`, `schema`, `agents-md`, `ref currencies|countries`,
`cache clear`.

All endpoints are reachable except `POST /order` with `SubmitOrder=true`, which is
unreachable by construction (D9), and `POST /order/CreateFromOrder` in its submitting form.
Deprecated v1 mfr variants are deliberately not exposed.
Search commands must never require the order key, so `bom price` runs with zero ability to
touch the account. That is a requirement, not an implementation detail.

Nothing in the API for: parametric search, cross-references/substitutes, BOM upload, or real
shipment tracking. `part alternates` and `bom price` are pure client-side composition, and
`order track` may be thin to the point of being worth cutting.

### `mouser bom price` is the product
A 60-line BOM at 30 req/min is two minutes wall clock, ~6% of the daily budget, and ~200KB of
JSON into an agent's context. `bom price` makes that one command, one cached pass, ~2KB out.
Rules that make it trustworthy:
- **No exact match:** never guess. `status:"unmatched"` plus top-3 scored candidates, run exits 9.
  Fix path is `bom resolve` writing a committed `bom.lock` of MPN to MouserPartNumber pins.
- **MOQ and multiples:** always compute `order_qty = max(Min, ceil(need/Mult)*Mult)` and always
  surface `need`, `order_qty`, `overbuy_units`, `overbuy_cost`. Silent rounding is how you lose
  trust at checkout.
- **Price breaks:** price at `order_qty`, and emit `next_break {qty, unit_price, delta_total}`.
- **Stock:** report availability, factory stock, lead time, lifecycle. EOL/NRND is a hard warning
  even when stock is fine. `--allow-alternates` adds tagged candidate rows, never replaces a line.
  Top-level `blockers[]` lists everything preventing a complete build.
Scope fence: one canonical CSV (`mpn,qty,ref[,manufacturer]`) plus KiCad's default BOM export,
with `--columns` remapping. No schematic parsing, no writing back to KiCad, no multi-distributor.

## 7. Scope

The cut line moved. With ordering gone, the whole point of v0.1 is the handoff, so
`bom to-cart` and `cart open` are promoted into it. Shipping a BOM pricer that cannot produce
a cart would be shipping half a product.

**v0.1, the complete loop:** `part search|get|price`, `bom price|resolve|to-cart`,
`cart show|add|update|remove|open`, `order get|history`, `auth status`, `doctor`, `schema`,
the output contract, exit codes, cache, cross-process throttle.

**v0.2:** `cart check`, `cart quote`, `order reorder`, `part alternates`, `cart replace`,
schedules, `ref *`, `order track`, `--human` tables.

**Never:** order submission in any form, MCP server mode, interactive TUI, multi-distributor
pricing, schematic parsing, credentials cached in the VM.

## 8. Distribution

### D10. Binary `mouser`, repo `mouser-cli`
PyPI `mouser` (sparkmicro/mouser-api) also installs a `mouser` executable, so PATH shadowing
is real for the small overlap of users who have both. Handling, rather than renaming:
- `mouser doctor` detects another `mouser` earlier on PATH and reports it with the resolved
  paths and a fix, instead of behaving mysteriously.
- Homebrew formula and the install script check for a conflicting `mouser` and warn at install
  time rather than silently winning or losing.
- The Homebrew formula is named `mouser-cli`, so the package names never collide even though
  the binaries do.
- Non-affiliation disclaimer in the README first screen and in `mouser --version` output.
- pix: a pack containing the binary, a `SKILL.md` teaching the workflow, a knowledge file of
  API gotchas (200-with-errors, full-replace cart, Min/Mult), config, `EXTRA_CLIS` registration,
  and the host-side credential step.
- Mac: `brew install <tap>/mouser-cli`. Linux/CI: `curl | sh` installer dropping a binary in
  `~/.local/bin`. Other harnesses: `go install`.
- **No MCP shim.** Every harness can run a shell command. A shim adds daemon lifecycle,
  protocol translation, and a second maintenance surface to save nothing.
- Disclaimer, required: not affiliated with, endorsed by, or sponsored by Mouser Electronics, Inc.

## 9. Build order (keys not provisioned yet)

The missing keys are a sequencing constraint, not a blocker. Roughly 80% of this design is
offline work, and doing it first means the day the keys land is spent on discovery rather than
plumbing.

**Phase 0, offline, needs no keys.** Everything that has no live dependency:
vendored swagger and generated DTOs, the view layer and field projection, the output envelope,
exit codes, `schema`, `agents-md`, BOM parsing and the `Min`/`Mult`/price-break arithmetic
(pure functions, unit-testable, and the part most likely to be quietly wrong), the credential
resolution chain and redaction sink, the cache and the `flock` rate ledger. The BOM math and
the redaction sink both get real test suites here, because neither needs a network.

**Phase 1, the probe, first hour after the keys arrive.** One scripted session that answers
every open question below and records cassettes for the whole suite in the process. Do this
before writing another line of client code, because two of the answers can delete a command.
The probe list:
1. Insert one cheap part with an empty `CartKey`, then look at mouser.com. Does the cart appear
   in Saved Carts? What URL reaches it? This decides the shape of `cart open`, the single most
   important command in the product.
2. Force a bad line (a nonexistent part number alongside a good one) and capture the per-line
   `CartItems[].Errors` shape.
3. `GET order/{orderNumber}` against a real past order. Is there usable carrier and tracking
   data, or is `order track` vapor to be cut?
4. Capture a real `Availability` / `LeadTime` / `FactoryStock` string set, since we are parsing
   those into ints and the formats are undocumented.
5. Confirm the `{version}` path segment's tolerance. The InvenTree plugin uses `v1.0` and `v001`
   interchangeably against the same API, which suggests it is loose; pin one and verify.
6. Confirm the search key genuinely cannot touch cart endpoints, which is the assumption behind
   letting `bom price` run with no account access.

**Phase 2:** wire the live client behind the cassettes, then the pix pack.

## 10. Open questions

1. **Does an API-created cart show up on mouser.com so you can click Buy?** This is now the
   load-bearing assumption of the entire product. Evidence for: the InvenTree supplier plugin's
   docs say carts it creates land in the supplier account and must be deleted through the web
   UI. Not yet proven: a deep link straight to that cart. Mouser's own help describes saved
   carts reachable at `/OrderHistory/CartsView.aspx` and, separately, an "Access ID" mechanism
   for unauthenticated cart retrieval, which may or may not be the same identifier as the API
   `CartKey`. Probe 1 settles it. Fallback if there is no deep link: `cart open` prints the
   cart key and sends you to the Saved Carts page. Worse, still fine.
2. **Does `GET order/{orderNumber}` return real tracking data?** If not, cut `order track`
   rather than shipping a disappointing alias for `order get`.
3. **pix pack specifics** (manifest schema, PATH symlinking, secret-to-env mapping) cannot be
   verified from inside the sandbox and must be checked against the host implementation.
