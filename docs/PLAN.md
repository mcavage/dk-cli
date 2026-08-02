# dk: plan

An agent-first CLI for DigiKey.

**The product in one line:** an agent builds a parts list, `dk` prices and sanity-checks it into
a terminal table you read, then hands you one URL that puts the parts in your DigiKey cart.
The CLI never places an order and holds no credential that could.

Status: plan only, no code written.

## 0. Decided

- **DigiKey, not Mouser.** See section 1. The Mouser plan is archived at
  `docs/archive/2026-02-mouser-plan-superseded.md`.
- **No autonomous ordering.** Manual checkout in the browser is the design, not a limitation.
  The Ordering API is out of scope permanently.
- **Review is a terminal table.** No HTML report, no web UI, no phone story.
- **Repo directory needs renaming** from `mouser-cli`. Binary name open, see D12.

## 1. Why DigiKey, not Mouser

The original plan targeted Mouser. Comparing the two APIs directly reversed the decision.
DigiKey wins on: proper HTTP status codes and RFC-7807 problem details (Mouser returns HTTP 200
with a populated `Errors[]`), rate limit headers on every response (Mouser publishes numbers and
tells you nothing), parametric search (Mouser has none at all), typed fields instead of strings
to parse, a substitutes endpoint, and server-side field projection.

**Correction to the original comparison:** "DigiKey has a real sandbox" was listed as a headline
advantage. It is largely false. DigiKey's sandbox always returns a canned example product and
exists only to verify auth plumbing (their FAQ says so outright), so it cannot test any of the
packaging, MOQ, pricing, or stock behavior this tool depends on. The remaining advantages stand
and the decision does not change, but this one should not have been counted.

Mouser's one structural advantage was a persistent server-side cart. That turned out not to
matter, because DigiKey has two zero-auth handoff paths that deliver the actual goal more
simply, and I verified one of them working from this sandbox. Mouser has no equivalent: its BOM
tool needs a login and a file upload, and `kitspace/1clickBOM` resorts to a browser extension
to fill Mouser carts, which is what you build when there is no URL to hit.

Auth also inverted. This design needs only 2-legged client credentials for product data, and
nothing at all for the handoff, so DigiKey's total credential surface is one client ID and one
secret, neither of which can spend money. Mouser required an account-scoped key that could
create carts and submit orders.

Non-API reason, which outranks all of the above: the user prefers to give DigiKey the money.

## 2. The API surface, verified

### Verified live from this sandbox
`POST https://www.digikey.com/mylists/api/thirdparty?listName=<name>&tags=<tags>` with a JSON
array of parts returns a single-use short URL. **No credentials, no account, no OAuth.** Three
findings, all of which affect the client:
1. **The documented response shape is wrong.** Docs say `{"singleUseUrl": "..."}`. The actual
   response is a bare JSON string, e.g. `"https://www.digikey.com/short/b3hdmm74"`. The parser
   must accept both, because they may fix it.
2. **`requestedPartNumber` accepts either a DigiKey part number or a manufacturer part number**,
   with an optional `manufacturerName`. **VERIFIED END TO END.** Pushing
   `1050-ABX00052-ND` rendered as that exact DigiKey part number, in Bulk packaging, at $29.40.
   Pushing `TL072CP` + `Texas Instruments` resolved correctly too. This is load-bearing: a
   packaging variation pinned in `bom.lock` (D4) is honored, not re-guessed by DigiKey's backend.
   Had it been MPN-only, the handoff would have discarded our packaging resolution and could turn
   a 10-piece cut-tape line into a 2000-piece reel.
3. **The single-use URL expires within minutes.** Observed: a URL that showed nothing, then a
   freshly minted one that worked. Design consequence in D14: mint and open atomically, never
   print a URL for the human to use later.

The rendered page also confirms what the handoff surfaces without any authenticated call:
stock and expected-restock date, packaging, MOQ, unit and extended price, lifecycle status,
REACH, ECCN, HTSUS, country of origin, and a **tariff notice** ("may apply if shipping to the
United States"). The tariff line is a real cost term and drove D4b.
3. **The URL is browser-only.** A headless fetch returns 403, correctly. The CLI's job ends at
   emitting the URL; the user's browser does the rest. This is why it needs no credentials.
Per-line `referenceDesignator` and `customerReference` ride along and appear on DigiKey's pick
labels, so `U1` / `R7` / `C12` survive the whole way.

Alternate handoff: **FastAdd**, `https://www.digikey.com/classic/ordering/fastadd.aspx` with
`part1`/`qty1`/`cref1`... which drops parts straight into the cart with no review page. It
requires **DigiKey** part numbers, and GET caps out around 1700 characters. Kept as a fallback
and as the `--direct` mode, not the default.

Both handoff paths are unversioned web endpoints published on a forum, not contract APIs. They
can change without notice. That risk is mitigated by keeping the handoff behind one interface
with two implementations, and by `dk doctor` probing it.

### Product Information v4 (the authed part)
`POST api.digikey.com/products/v4/ProductSearch/KeywordSearch` plus ProductDetails,
ProductPricing, ProductSubstitutes, ProductAssociations, Categories, Manufacturers.
- Auth is **OAuth2 2-legged client credentials**. `POST https://api.digikey.com/v1/oauth2/token`
  with `client_id`, `client_secret`, `grant_type=client_credentials` as
  `application/x-www-form-urlencoded`. Response carries `access_token`, `expires_in`,
  `token_type`. Then every call sends `Authorization: Bearer <token>` **plus** an
  `X-DIGIKEY-Client-Id` header (both are required; the Bearer prefix is mandatory or you get a
  bearer token error). No browser redirect, no refresh token, no 3-legged flow.
- **Access tokens expire in 10 minutes** (documented; the example response shows
  `expires_in: 599`). This is much shorter than typical and it drives D15: with an agent invoking
  the binary dozens of times across a session, most invocations will hit a valid cached token but
  refreshes will be frequent, and concurrent invocations must not stampede the token endpoint.
- Sandbox is the same flow against host `sandbox-api.digikey.com`, with its own separately
  registered application and its own credentials.
- An application must be **subscribed to an API Product** (Product Information V4) before calls
  succeed. Registration alone is not enough.
- Locale is set by headers: `X-DIGIKEY-Locale-Site`, `-Language`, `-Currency`.
- **The sandbox is auth-only.** It always returns the same canned example product regardless of
  the query; its documented purpose is verifying that a client can authenticate and communicate.
  It cannot validate field mappings, packaging variations, MOQ, pricing, or stock. There are also
  open reports of correctly-configured sandbox apps returning 403. Sandbox apps are created under
  a personal developer account (listed simply as "Apps"), not under an organization, which is
  where Production Apps live.
- **`KeywordSearch` data may be up to 24 hours stale** (DigiKey's FAQ says so and directs you to
  `ProductDetails` for real-time pricing and availability). This is load-bearing, see D16.
- **Server-side field projection exists** via an `Includes` parameter naming the fields to return.
  Worth using: it shrinks payloads before they are parsed rather than after.
- **VERIFIED on a live account:** one `ProductDetails` call returns every packaging variation
  inline, each with its own `MinimumOrderQuantity`, `StandardPackage`,
  `QuantityAvailableforPackageType`, `DigiReelFee`, and full `StandardPricing` ladder.
  `RC0805FR-0710KL` returned 3 variations and 22 price breaks in a single response. So the
  packaging policy (D4) costs no extra API calls, and a 60-line BOM stays near 60 calls.
  Also confirmed: token flow works, the app subscription is live (HTTP 200), and
  `x-ratelimit-limit: 1000` / `x-ratelimit-remaining: 999` come back as documented.
- Free tier is self-serve at 1000 calls/day with no approval, and `X-RateLimit-Limit` /
  `X-RateLimit-Remaining` come back on every response.
- Errors are real status codes (400/401/403/404/429/500/503) with a problem-details body
  carrying a `correlationId`.
- Typed fields worth having: `QuantityAvailable` (int), `MinimumOrderQuantity`,
  `StandardPackage`, `ProductStatus`, and explicit booleans `Discontinued`, `EndOfLife`,
  `Ncnr`, `BackOrderNotAllowed`, `NormallyStocking`. Plus `ManufacturerLeadWeeks`,
  `DateLastBuyChance`, and `Classifications` (RoHS, REACH, MSL, ECCN, HTSUS).
- Search returns faceted `FilterOptions` including `ParametricFilters` with product counts,
  and accepts `ParameterFilterRequest` to filter by them.

### Explicitly out of scope
The Ordering API (also requires an active DigiKey Credit account), MyLists authenticated
endpoints (3-legged OAuth, and the zero-auth path already covers the need), Quote, Barcode,
Bonded Inventory. Order Status is a maybe, see Q2.

## 3. Decisions

### D1. Go, single static binary
Stdlib covers this. ~5-10ms cold start matters when an agent invokes it 40 times a session.
Cross-compiles, installs unprivileged, no runtime. Rejected: Node/npx (200-600ms boot per
call), Python/uv (same plus interpreter assumptions), Rust (buys nothing here, costs a solo
maintainer). Carried over unchanged from the Mouser plan.

### D2. Vendored OpenAPI, committed generated DTOs, hand-written domain layer
DigiKey publishes downloadable swagger per endpoint. Vendor it into `api/spec/`, generate DTOs
with `go generate`, **commit the output** so it is reviewable. Never fetch a spec at build time.
A scheduled CI job diffs the live spec against the vendored copy and fails loudly on drift.
Decoders tolerate unknown fields; raw response bytes are retained so `--format raw` never loses
a field DigiKey added yesterday.

### D3. Two-layer output, ~13-field default view
DigiKey's `Product` is large and nested (`ProductVariations[]` alone can be dozens of objects).
Wire DTO keeps full fidelity; a flat View is the default, snake_case, stable names.

Default search record: `dk_pn` (the chosen variation's DigiKey number), `mpn`, `manufacturer`,
`description` (truncated), `stock`, `moq`, `std_package`, `lead_weeks`, `unit_price` +
`currency` at the requested qty, `status` (normalized Active/NRND/EOL/Discontinued/Obsolete),
`ncnr` (bool), `datasheet_url`, `product_url`.

Dropped by default: the full `Parameters[]` array, all non-selected variations, the pricing
ladder, media links, category taxonomy, classifications. Escape hatches: `--fields a,b,c`,
`--price-breaks`, `--params`, `--variations`, `--full`, `--format raw`.

### D4. Packaging variation selection is an explicit, named policy
This is new versus Mouser and it has money attached. One MPN maps to several DigiKey part
numbers (cut tape, tape and reel, DigiReel), each with its own MOQ, `StandardPackage`, price
ladder, and possibly a `DigiReelFee`. Picking silently would be wrong.

Default policy: **lowest landed total after MOQ forcing**, among variations that are in stock and
orderable, where

    order_qty     = MOQ-forced quantity for that variation (see D4c)
    landed_total  = order_qty * unit_price_at(order_qty) + flat_fees(variation)

and ties break toward the lower MOQ. **"Cheapest at the requested quantity" is the wrong rule and
was the original wording here.** Real observed data for `RC0805FR-0710KL`:

| DigiKey PN | packaging | MOQ | std pkg | reel fee |
|---|---|---|---|---|
| `311-10.0KCRTR-ND` | tape and reel | **5000** | 0 | 0 |
| `311-10.0KCRCT-ND` | cut tape | 1 | 0 | 0 |
| `311-10.0KCRDKR-ND` | DigiReel | 1 | 1 | **7.00** |

All three are in stock in the millions, so all three pass an "available and orderable" filter.
The tape-and-reel unit price at 5000 units is far below the cut-tape price at 10 units, so any
rule that compares unit prices, or compares totals at the *requested* quantity rather than the
*forced* quantity, buys 5000 resistors when the user asked for 10. Comparing landed totals after
MOQ forcing rejects it by roughly 80x. Cut tape and DigiReel are otherwise identical on MOQ and
unit price, so the $7 fee is the only thing distinguishing them, which is why D4a exists. Every priced line reports which variation was chosen and why, and
`--packaging <cut-tape|reel|digireel|any>` forces it. `--variations` shows the alternatives that
were rejected. Never select a variation whose MOQ exceeds the requested quantity without saying
so in the overbuy fields.

This costs no extra API calls. `KeywordSearch` returns `ProductVariations[]` inline, and each
variation already carries its own `StandardPricing[]` ladder, `MinimumOrderQuantity`,
`StandardPackage`, `QuantityAvailableforPackageType`, and `DigiReelFee`. One call per BOM line
is enough to price every variation of it. A hardcoded packaging preference order was considered
and rejected: the data to choose correctly is already in the payload, so a heuristic would
sometimes pick a more expensive variation than the one sitting next to it in the response.

### D4b. Tariffs and restock dates are flags, not footnotes
Two money-and-time terms surfaced by a real page render that the first draft ignored.

**Tariffs.** v4 exposes `TariffActive` per variation plus tariff information, and the web page
warns that tariffs "may apply if shipping to the United States", which is where this user ships.
The CLI cannot compute the tariff amount, so it must not pretend to. It surfaces `tariff: true`
as a table flag and a warning code, so a total is never presented as final when a tariff applies.
Silence here would be the worst option: the user finds out at checkout.

**Stock zero with a future restock date.** A real observed line: `0 In Stock`, `1 expected in
stock on 05-Aug-2026`, displayed calmly next to a normal price. This is how you buy a part that
arrives in six months. Any line with `QuantityAvailable` below the needed quantity is a
`blockers[]` entry carrying the expected date, never a footnote, and it trips the D13 push gate.
`NormallyStocking`, `BackOrderNotAllowed`, and `ManufacturerLeadWeeks` feed the same judgement.

### D4c. The MOQ formula must survive `StandardPackage == 0`
The original formula was
`order_qty = max(MinimumOrderQuantity, ceil(need / StandardPackage) * StandardPackage)`.

**`StandardPackage` is 0 in real responses**, observed on two of three variations of
`RC0805FR-0710KL` (both the tape-and-reel and the cut-tape options). In Go that formula is an
integer divide-by-zero panic, on the most commonly ordered packaging type. Corrected:

    if StandardPackage > 0:
        order_qty = max(MOQ, ceil(need / StandardPackage) * StandardPackage)
    else:
        order_qty = max(MOQ, need)          # no packaging multiple constraint

Zero means "no multiple required", not "multiple of zero". `RC0805FR-0710KL` becomes a permanent
test fixture covering all three cases in one part: MOQ 5000 with std pkg 0, MOQ 1 with std pkg 0,
and MOQ 1 with std pkg 1 plus a flat fee. Any part where MOQ, need, or std pkg is absent, null,
or negative is a hard error on that line, never a silent default to 1.

### D4a. Flat per-line fees are a first-class term
`DigiReelFee` is a discrete field and a flat charge per line item, **observed as exactly 7.00**
on `311-10.0KCRDKR-ND`. It does not appear in unit price, so any arithmetic that only multiplies
unit price by quantity under-reports the real total. Cut tape and DigiReel for that part are
identical on MOQ and unit price, so without fees in the comparison the tool would pick DigiReel
on a tiebreak roughly half the time and silently add $7 per line, which on a 40-line BOM is $280.

Therefore: fees are a named term in the line total, in `overbuy_cost`, and in the D4 variation
comparison, never folded into a unit price. The terminal table grows a `fees` column whenever
any line carries one, and the totals block reports total fees separately. A DigiReel variation
that is cheapest per unit but loses on landed total must lose.

### D5. JSON always, no TTY sniffing
Format is a function of explicit input only: JSON by default for everyone, `--table` for the
human view, `DK_OUTPUT=table` as the env override. When stdout is a TTY and no format was
chosen, still print JSON and put `tip: add --table` on **stderr**. TTY detection is the top
source of "worked in my shell, broke in the agent".

One envelope for every command:
```
{"ok":true,"command":"part.search","data":{...},"warnings":[],"meta":{...}}
{"ok":false,"command":"bom.price","error":{"code":"NO_MATCH","message":"...","retryable":false,"fix":"<runnable command>","details":{"upstream":{...,"correlationId":"..."}}}}
```
`error.code` is a stable enum. `error.fix` is a literal runnable command or null. DigiKey's
`correlationId` is always preserved in `error.details`, because it is what support asks for.

Exit codes: 0 ok, 2 usage, 3 credential, 4 not-found/ambiguous, 5 upstream rejection,
6 rate limit, 7 network, 8 destructive-refused, 9 partial (data usable), 1 internal.
The rule agents are taught: **0 or 9 means `data` is usable, anything else means read
`error.fix`.** Exit 9 is load-bearing, because BOM pricing is inherently partial.

### D6. The review artifact is a terminal table
`bom price --table` is the thing the human actually reads before spending money. Requirements:
one row per BOM line, and the columns that catch agent mistakes are not optional:
refdes, mpn, matched dk_pn, need qty, order qty, unit price, line total, and a flags column
carrying MOQ overbuy, EOL/NRND, NCNR, backorder, and low stock. Below the table: a totals block
(merchandise total, and total overbuy cost called out separately as money you did not plan to
spend), then a `blockers` list, then the unmatched lines with their candidates.

It must render legibly at 100 columns, degrade without color when `NO_COLOR` is set or stdout
is not a TTY, and never require horizontal scrolling to see the flags column. No HTML output,
no browser-based report, no pager. Explicitly cut.

### D7. Discoverability is two artifacts, one source of truth
`dk schema` emits the whole command surface as one JSON document: commands, flags with types
and enums and defaults, the exit code table, the envelope shape, one example per command.
`--compact` drops prose. Plus a shipped `AGENTS.md`, also emittable as `dk agents-md`, carrying
the rules a schema cannot express. Human `--help` renders from the same schema data so they
cannot drift. Rejected: per-subcommand `--help --json`, which is N invocations to learn N
commands, exactly the token burn being avoided.

### D8. Never truncate silently
Every list response carries `meta.page = {returned, total_upstream, offset, limit, has_more,
next_command}` where `next_command` is a literal executable string, since DigiKey search is
`Limit`/`Offset` based. Truncation also pushes a warning code, so an agent skimming only
`warnings` still sees it. Field projection is truncation too: `meta.fields = {mode:"summary",
omitted:N, full:"--fields all"}`. Same for suppressed packaging variations (D4).

### D9. Rate limiting from headers, cache by data class
Read `X-RateLimit-Remaining` off every response and persist it. This deletes the guessed
`flock` ledger the Mouser plan needed. Still keep a small state file so concurrent invocations
see the last known remaining count, and refuse to start a large `bom price` run whose estimated
call count exceeds what is left, with the estimate in the error rather than half-completing.

XDG paths: config in `$XDG_CONFIG_HOME/dk`, responses in `$XDG_CACHE_HOME/dk`, tokens and
rate state in `$XDG_STATE_HOME/dk` (0600). Cache key is
`sha256(cli_version + endpoint + normalized_body + locale + client_id_fingerprint)`.
TTL: search and product details 24h, categories and manufacturers 30d, pricing 1h (it moves and
it is money), nothing about a specific order ever. `--refresh`, `--no-cache`.
Every response reports `meta.cache = {hit, age_s, ttl_s, stale}`. Pricing refuses to serve
stale data under rate limiting rather than quoting an old price.

### D16. `ProductDetails` for money, `KeywordSearch` for discovery
`KeywordSearch` responses may be up to 24 hours stale. Pricing a BOM off day-old stock and
prices while presenting it as current is precisely the quiet wrongness this tool exists to
prevent: a part with 3 left in stock, or a price that moved, would be reported confidently wrong.

So the two endpoints have separate jobs and are not interchangeable:
- `KeywordSearch`: discovery, matching, candidate ranking, parametric filtering. Cacheable for
  24h because it is already cached upstream anyway.
- `ProductDetails`: every number that informs a purchase. One call per resolved BOM line.
  Short cache TTL (1h), and `bom price` reports `data_age_s` per line so staleness is visible
  rather than implied.

Call budget consequence: a 60-line BOM costs ~60 `ProductDetails` calls plus whatever matching
required, against a 1000/day quota. Acceptable, but it makes the response cache load-bearing
rather than a nicety, and it makes `bom resolve` (which pins parts once) worth having so that
re-pricing an already-resolved BOM skips the matching calls entirely.

### D10. Record/replay cassettes, because the sandbox cannot test behavior
The sandbox is an auth check that returns a canned product, so it validates the token flow and
nothing else. Use it for exactly that, in one test, and do not build a strategy on it.

Real strategy, same as the archived Mouser plan needed:
- **Cassettes.** `DK_CASSETTE_DIR` records real production responses once; the suite then runs
  offline against them. This is the only way to test packaging selection, MOQ math, and fee
  arithmetic against real shapes without burning quota on every CI run.
- **Contract tests** run on a schedule (not every commit) against production with a tiny quota
  footprint, asserting the fields this tool depends on still exist with the expected types:
  `MinimumOrderQuantity`, `StandardPackage`, `QuantityAvailable`, `DigiReelFee`, `TariffActive`,
  and the status booleans. A DigiKey change should break a scheduled test, not a user's BOM.
- **Cassettes must be scrubbed** of tokens and client IDs before commit, enforced by a test.
- The zero-auth handoff gets a live smoke test, since it has no sandbox at all and its response
  shape already differs from its documentation.

### D11. The CLI cannot spend money, by construction
No ordering API client exists in the binary. No 3-legged OAuth flow exists, so the tool holds
no credential that could act on the account. The only credentials are a client ID and secret
scoped to product data, and the handoff path is unauthenticated by design. The worst outcome of
a hallucinating agent, a bad BOM parse, or a wrong MOQ calculation is a cart you look at and
reject. Reviewing a wrong cart costs thirty seconds; every failure mode is recoverable by
closing a browser tab.

The one destructive-ish flag: FastAdd `--direct` adds to your **existing** cart, and
`newcart=true` starts a fresh one. `--direct` prints exactly what it is about to add and
requires `--yes`, because it skips the review page.

### D12. Naming, open
Repo dir is currently `mouser-cli` and must be renamed on the host. Options for the binary:
- `dk`: two characters, cheapest for agents and humans, no affiliation implied. Worth checking
  for collisions before committing (Docker has experimented with a `dk` command).
- `dkey`: nearly as short, collision-free, slightly awkward.
- `digikey`: clearest, but implies affiliation and is more to type.
Recommendation: `dk`, repo `dk-cli`, module `github.com/<user>/dk-cli`. If a later Mouser
binary happens, it is a sibling binary in the same repo sharing the BOM and output libraries,
never a `--distributor` flag, because that flattens every command to a lowest common
denominator and would cost DigiKey's parametric search.
Non-affiliation disclaimer in the README first screen and in `dk --version`.

## 4. Credentials

Much simpler than the Mouser design, because DigiKey uses a `Bearer` header rather than an API
key in the query string. **The entire class of "a URL is a credential" leaks is gone**: no
redaction of query strings, no scrubbing URLs out of error messages, no risk of a key landing
in a proxy access log.

Two secrets: `DK_CLIENT_ID` and `DK_CLIENT_SECRET`. Resolution chain, first hit wins, source
name (never value) reported on `--verbose`:
1. `DK_CLIENT_ID` / `DK_CLIENT_SECRET` env vars. **If a value starts with `op://`, resolve it
   with `op read`.** This makes 1Password primary without hardcoding it and without requiring
   `op` where it does not exist.
2. `op read` of secret references declared in config.
3. OS keychain (`security` / `secret-tool`).
4. Plaintext in config, accepted only at mode 0600, warn otherwise.

`op read` beats the alternatives: `op run` is the wrong shape for a CLI resolving its own
config, `op item get` returns more than needed, and a service-account token is itself a
credential at rest granting vault-wide read.

Still prohibited: no `--client-secret` flag (flags land in shell history, `ps`, and agent
transcripts), never log a token or a secret, and the redaction sink stays (a single sink that
scrubs both secret values and any bearer token from every emitted line) because access tokens
still exist in memory and in `Authorization` headers that a verbose HTTP dump would print.

### D15. Token cache sized for a 10-minute token
Tokens live 10 minutes, so the cache is not an optimization, it is required: without it a
40-invocation agent session would hit the token endpoint 40 times.

`$XDG_STATE_HOME/dk/token.json`, mode 0600, holding the access token and an absolute expiry.
Refresh when expired or within a 30s skew (a 60s skew against a 600s token wastes 10% of its
life). The write is atomic (temp file plus rename) and a `flock` guards refresh so concurrent
invocations coalesce onto one token request rather than stampeding. A refresh failure must be
distinguishable from an API failure in the error envelope, because the fixes are different: bad
credentials versus an unsubscribed app versus a network problem.

Sandbox and production credentials are separate applications, so the token cache and the
response cache are both keyed by environment. A sandbox token must never be usable against
production, and cached sandbox responses must never be served to a production query.

### pix wiring
Host-side `op read` at provision time, injected with `sbx secret set`, surfaced in-VM as the
two env vars. The CLI in-VM is unmodified and just reads env. Matches the existing house
pattern for model keys and the GitHub token, needs no new infrastructure. `op` never goes in
the VM.
Residual risk, accepted: the client ID and secret are readable by code running in the sandbox.
Blast radius is product-data reads against a 1000/day quota. Nothing in the account is exposed
and nothing can be purchased. This is a materially smaller risk than the Mouser design carried.

## 5. Command surface

Global: JSON default, long flags only, never interactive, `--table` for humans.

**part**: `search <keyword>`, `get <mpn>`, `price <mpn> --qty N`, `alternates <mpn>` (real,
backed by ProductSubstitutes), `params <category>` (list filterable parameters and their values,
the discovery step for parametric search).

**bom**: `price <file>` (the report), `resolve <file> -o bom.lock` (pin MPN to DigiKey part
number and packaging variation), `push <file>` (mint the single-use URL; `--open` launches the
browser; `--direct` uses FastAdd with `--yes`). `push` is gated, see D13.

**ref**: `categories`, `manufacturers`.

**meta**: `auth status`, `doctor`, `schema`, `agents-md`, `cache clear`.

No order commands. No cart commands, because DigiKey has no cart API and the local `bom.lock`
plus the handoff URL is the cart.

### `dk bom price` is the product
Everything else is an HTTP client with nicer flags. This is the command that turns 60 API
responses into one decision. Rules that make it trustworthy:
- **No exact match: never guess.** `status:"unmatched"` plus top-3 scored candidates, run exits
  9. Fix path is `bom resolve` writing a committed `bom.lock` that pins part number and
  packaging variation, so the next run is deterministic.
- **MOQ and standard package:** compute per D4c, and always surface `need`, `order_qty`,
  `overbuy_units`, `overbuy_cost`. Silent rounding is the
  fastest way to lose trust, and you discover it at checkout when the total is higher than the
  report said.
- **Price at `order_qty`, never at `need`.** Also emit `next_break {qty, unit_price,
  delta_total}` so the table can say "18 more units drops the unit price 22% for $3.10 more".
- **Status is a hard warning even when stock is fine:** `EndOfLife`, `Discontinued`, `Ncnr`
  (non-cancellable non-returnable, which for a hobbyist is a real trap),
  `BackOrderNotAllowed`, `NormallyStocking` false, and `DateLastBuyChance`.
- **`blockers[]`** at the top level lists every line preventing a complete build.

### D14. Mint and open atomically
The single-use URL expires within minutes (verified). So `bom push` mints the URL and hands it
to the browser in one action. `--open` is therefore the default behavior, not an opt-in, and the
printed URL is labeled as already-used or short-lived. `--print-only` exists for piping and
debugging but warns that the URL will likely be dead by the time a human reads it. A stale URL
renders as an empty page rather than an error, which is a confusing failure mode, so `doctor`
checks the handoff by minting and immediately fetching one, and the error text for an empty
render explicitly names expiry as the likely cause.

### D13. `bom push` refuses a broken BOM
Exit 9 ("partial, data usable") is correct for `bom price`, but it creates a hole: an agent told
"price this and get me a cart" sees exit 9, reads it as success, runs `push`, and hands over a
URL for an unbuildable cart. The human assumes the agent succeeded and buys it.

So `push` is not a thin wrapper over the handoff. It re-reads the priced report and **hard
refuses** (exit 8) when any line is unmatched, any `blockers[]` entry exists, any part is
EndOfLife or Discontinued, or `overbuy_cost` exceeds a configurable threshold. Overriding needs
`--force`, which prints the exact list of what is being overridden before emitting the URL.
There is no path from a broken BOM to a cart that does not pass through a human reading the
reasons. Note this is a check on OUR report, not on DigiKey, so it costs nothing and cannot be
skipped by an agent that never ran `price`: `push` requires a report or lockfile and will not
synthesize one silently.
- `--allow-alternates` adds substitute candidate rows tagged as alternates and never replaces
  a line.

Input scope fence: one canonical CSV (`mpn,qty,refdes[,manufacturer]`) plus KiCad's default BOM
CSV export, with `--columns mpn=MPN,qty=Qty` remapping for anything else. No `.kicad_sch`
parsing, no writing back into KiCad, no multi-distributor.

Edge cases that are requirements, not afterthoughts: empty file; duplicate MPNs across refdes
(merge, sum, report the merge); qty 0 or non-numeric; DNP rows (skip and report the skip); a
400-line BOM against the remaining daily quota (refuse with the estimate, do not half-complete);
a line whose MOQ exceeds what you want (report overbuy loudly, never drop the line).

## 6. Scope

**v0.1, the complete loop:** `part search|get|price`, `bom price|resolve|push`, `auth status`,
`doctor`, `schema`, `agents-md`, the output envelope, exit codes, the terminal table, token
cache, response cache, rate-limit awareness, packaging variation policy.

**v0.2:** `part alternates`, `part params` plus parametric filtering on `search`,
`ref categories|manufacturers`, `bom push --direct` (FastAdd), `--allow-alternates`.

**Never:** placing orders, 3-legged OAuth, MCP server mode, interactive TUI, HTML or web
reports, multi-distributor pricing, schematic parsing, credentials cached in the VM.

## 7. Distribution

- pix: a pack containing the binary, a `SKILL.md` teaching the loop, a knowledge file of API
  gotchas (the undocumented bare-string response, packaging variations, NCNR), config,
  `EXTRA_CLIS` registration, and the host-side credential step.
- Mac: `brew install <tap>/dk-cli`. Linux/CI: `curl | sh` dropping a binary in `~/.local/bin`.
  Other harnesses: `go install`.
- **No MCP shim.** Every harness can run a shell command. A shim adds daemon lifecycle,
  protocol translation, and a second maintenance surface to save nothing.
- README first 30 lines: one-line mission, non-affiliation disclaimer, the three-command happy
  path, the output guarantee, the two env vars, `dk schema` for discovery, and the fact that it
  cannot place orders.

## 8. Build order

No waiting on anybody. DigiKey registration is self-serve and immediate, and the handoff needs
no credentials at all.

**Phase 0, no credentials needed.** BOM parsing, the MOQ / price-break / fee arithmetic as pure
functions with real unit tests, the output envelope, exit codes, `schema`, `agents-md`, the
terminal table renderer, and the handoff client (unauthenticated and already verified working).

**`push` is NOT a user-facing command at the end of Phase 0.** The tempting version of this plan
shipped "parse a BOM, push it, click buy" early, which is precisely the unpriced, unchecked cart
that the rest of the design exists to prevent, and it would train the user to trust a path with
no warnings in it. The handoff is built and smoke-tested in Phase 0 but stays behind a dev flag
until pricing exists to gate it (D13).

**Phase 1.** Register the app, wire 2-legged OAuth and the token cache, point at sandbox, build
the search and pricing client, then the packaging variation policy (D4/D4a) and contract tests.

**Phase 2.** Join them: `bom price` against live data, `push` gated on the report, then the pix
pack.

## 9. Open questions

1. **Binary name.** `dk` vs `dkey` vs `digikey`, and the repo directory needs renaming off
   `mouser-cli` on the host.
2. **Order Status API with 2-legged auth.** DigiKey documents it as supporting both 2- and
   3-legged. If 2-legged works, `dk order history` becomes possible with no new credential
   class, which would be a nice-to-have for "what did I buy last time". If it needs 3-legged,
   it is cut. Cheap to probe once registered.
3. RESOLVED. One `ProductDetails` call returns all variations with MOQ, standard package, fees,
   and full price ladders. See the verified note in section 2.
4. **pix pack specifics** (manifest schema, PATH symlinking, secret-to-env mapping) cannot be
   verified from inside the sandbox and must be checked against the host implementation.
