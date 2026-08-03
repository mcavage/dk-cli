# dk: rules for an agent

`dk` prices and sanity-checks a bill of materials against DigiKey, then hands
you one URL that puts the parts in a human's cart. **It cannot place an
order.** There is no ordering credential in this binary; the worst outcome of
a bad BOM or a wrong quantity is a cart a human looks at and rejects.

## The one rule

Every command prints exactly one JSON envelope on stdout and nothing else.
Progress and hints go to stderr. Read `ok` and the exit code, not prose:

- Exit 0 or 9: `data` is usable. 9 means partial (some lines unmatched,
  blocked, or unresolved) — check `warnings` before treating it as clean.
- Any other exit: `data` is absent. Read `error.fix`. It is a literal,
  runnable command or an empty string, never prose you have to interpret.

Run `dk schema` once at the start of a session for the complete command
surface (flags, types, defaults, exit codes, one example each). Do not probe
individual `--help` output N times; `dk schema` is the same data in one call.

## Rules a schema cannot express

- **Never guess a part.** An unmatched BOM line stays unmatched. Do not
  substitute a similar-looking part number and re-run; that is exactly the
  failure mode this tool exists to prevent. Fix the BOM or its `--columns`
  mapping instead.
- **Price at order quantity, not requested quantity.** `bom price` reports
  `need` vs `order_qty` separately because DigiKey's minimum order quantity
  and packaging multiples frequently force a larger buy. `overbuy_units` and
  `overbuy_cost` are real money the human did not ask to spend — never round
  past them silently in a summary you write for a human.
- **A tariff flag or an EOL/discontinued/NCNR flag is not decorative.** These
  ride in `warnings` and in each line's `flags`/`blockers`. Surface them to
  the human verbatim; do not drop them to make a summary shorter.
- **`bom push` refuses by default (exit 8, `REFUSED_UNSAFE`).** It will not
  synthesize a priced report on its own. Run `dk bom price <file> --table`
  first so a human can review it, then re-run `bom push --force` only after
  that review, never automatically in response to a refusal.
- **The push URL expires in minutes.** Do not cache it, do not hand it to
  the human minutes later, do not retry with the same URL. Mint again if a
  URL goes stale.
- **`--table` is the one command whose stdout is not JSON.** It exists so a
  human can read `bom price` directly; do not parse its stdout as JSON, and
  do not pass `--table` when you intend to parse the response yourself.
- **Only `part search`, `part get`, `part price`, and `bom price` need
  credentials** (`DK_CLIENT_ID` / `DK_CLIENT_SECRET`). Everything else,
  including `bom push`, works with none set. `dk doctor` tells you which
  credential source resolved, never the value.
- **`part search` results may be up to 24h stale** (DigiKey's own
  KeywordSearch caching). Never quote a search result's price or stock to a
  human as current; re-check with `part get` or let `bom price` do it.

## Recovery

- `MISSING_ARG` / `UNKNOWN_FLAG`: read `error.details.did_you_mean` if
  present, otherwise `error.details.valid` for the full list, then retry
  with `error.fix`.
- `NO_CREDENTIALS`: set `DK_CLIENT_ID` and `DK_CLIENT_SECRET`, or ask the
  human to. Do not invent placeholder values.
- `RATE_LIMITED`: stop retrying immediately; wait for the daily quota to
  reset rather than looping.
- `REFUSED_UNSAFE`: this is `dk` protecting the human from a bad cart, not a
  bug. Read `error.details.reasons`, fix the BOM, or get explicit human
  confirmation before adding `--force`.

## Non-affiliation

`dk` is an independent, unofficial tool. It is not affiliated with, endorsed
by, or supported by DigiKey Electronics.

## Help is for humans, `schema` is for you

`dk help`, `dk --help` and a bare `dk` print human text, not an envelope. That
is the one place this tool does not emit JSON, and it is deliberate: nobody
pipes `--help` to a parser.

Run `dk schema` once at the start of a session. It returns the entire command
surface, every flag with its type and default, the exit code table and the
envelope shape, in one call. Do not call `--help` per subcommand to learn the
tool; that is N calls to learn what one call already told you.

`dk help --json` is an alias for `dk schema` if you land there by accident.
