# feedmerge

A single Go binary that fetches many RSS and Atom feeds concurrently, normalizes
and deduplicates their entries, and serves one merged feed over HTTP as RSS 2.0,
Atom 1.0 and JSON Feed 1.1.

The core is standard library only. The RSS and Atom parsers are written here,
not pulled from a dependency, which is what makes the date handling, CDATA,
relative URL and identity-fallback behaviour below possible to reason about.

## Why merge feeds server-side

Feed readers can subscribe to many feeds, but doing the merge in one place buys
things a reader cannot:

- **One subscription for many sources.** Anything that speaks RSS - a reader, a
  chat bot, a static site build, a `cron` script - subscribes to a single URL.
- **Deduplication.** A story that appears in a project blog, an aggregator and a
  mailing list archive shows up once, not three times.
- **Filtering happens once.** Sponsored posts, release-candidate churn or an
  entire noisy category are dropped before anyone downstream sees them.
- **Politeness and caching.** One process fetches each upstream on a schedule,
  rate limited per host, and revalidates with `If-None-Match` /
  `If-Modified-Since`. Ten readers behind the merge cause one upstream request,
  not ten.
- **Normalization.** Downstream consumers get one format, one date encoding and
  absolute URLs regardless of how sloppy the upstream document was.

## Install

Requires Go 1.22 or newer.

```sh
go install github.com/roxiproject/feedmerge/cmd/feedmerge@latest
```

Or from a checkout:

```sh
git clone https://github.com/roxiproject/feedmerge
cd feedmerge
go build -o feedmerge ./cmd/feedmerge
```

## Usage

```
feedmerge serve --config feeds.yaml [--addr :8080] [--refresh 10m] [--quiet]
feedmerge fetch <url> [--format summary|rss|atom|json] [--limit N] [--timeout 20s]
feedmerge version
```

`serve` runs the merge loop and the HTTP server. `fetch` is a one-off inspector:
it retrieves a single feed and prints what the parser made of it, which is the
fastest way to find out why an entry has the date or identity it does.

```
$ feedmerge fetch https://go.dev/blog/feed.atom --limit 2
format:  atom (HTTP 200)
title:   The Go Blog
link:    https://go.dev/blog
updated: 2024-05-02T00:00:00Z
entries: 24

  1. Structured Logging with slog
     https://go.dev/blog/slog
     2023-08-22T00:00:00Z  id=tag:blog.golang.org,2013:blog/slog (guid)
  2. Deconstructing Type Parameters
     https://go.dev/blog/deconstructing-type-parameters
     2023-09-26T00:00:00Z  id=tag:blog.golang.org,2013:blog/... (guid)
```

## Configuration

The config file is either JSON (`.json` extension) or the small YAML subset
feedmerge parses itself. The subset supports nested mappings, sequences of
scalars, sequences of mappings, single- and double-quoted scalars, `#` comments
and blank lines. Anchors, flow collections (`{a: 1}`), multi-line scalars and
multiple documents are **not** supported and are reported as errors rather than
silently misread. Indentation must use spaces.

A complete example (also in [`feeds.example.yaml`](feeds.example.yaml)):

```yaml
# Metadata for the merged feed itself.
title: Go and Systems Reading
link: https://example.org/reading
description: A merged feed of Go, distributed systems and release announcements.
self_link: https://example.org/reading/feed.xml

addr: ":8080"
refresh: 15m          # how often to re-fetch every source
timeout: 20s          # per-feed HTTP timeout
workers: 8            # concurrent fetches
host_interval: 1s     # minimum delay between requests to the same host
max_items: 200        # cap on the merged output
user_agent: "feedmerge/1.0 (+https://example.org/reading)"

# Deduplication tuning.
title_threshold: 0.9  # Jaccard similarity at which two titles are "the same"
title_window: 72h     # only match by title within this time distance

filters:
  - exclude title ~ /\b(sponsored|advertisement)\b/i
  - include any ~ /(golang|distributed systems|postgres)/i

feeds:
  - url: https://go.dev/blog/feed.atom
    name: The Go Blog
  - url: https://www.postgresql.org/news.rss
    name: PostgreSQL News
  - https://example.com/plain-url-form.xml
```

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `title` | string | `feedmerge` | Title of the merged feed |
| `link` | string | - | Home page link of the merged feed |
| `description` | string | `Merged feed` | Feed description / subtitle |
| `self_link` | string | - | Public URL of the merged feed (`rel="self"`, `feed_url`) |
| `addr` | string | `:8080` | Listen address; `--addr` overrides it |
| `refresh` | duration | `15m` | Interval between merges; `0` disables the loop |
| `timeout` | duration | `20s` | Per-feed HTTP timeout |
| `workers` | int | `8` | Maximum concurrent upstream fetches |
| `host_interval` | duration | `1s` | Minimum delay between requests to one host |
| `max_items` | int | `200` | Entries kept after merging; `0` means unlimited |
| `user_agent` | string | `feedmerge/1.0 (...)` | `User-Agent` sent upstream |
| `title_threshold` | float | `0.9` | Title similarity required to call two entries duplicates; `0` disables |
| `title_window` | duration | `72h` | Maximum publication-time distance for a title match |
| `filters` | list of strings | - | Filter DSL rules |
| `feeds` | list | required | Upstream feeds, as `{url, name}` or a bare URL string |

Durations accept Go syntax (`90s`, `1h30m`) or a bare number of seconds. The
same keys work in JSON:

```json
{
  "title": "Go and Systems Reading",
  "refresh": "15m",
  "filters": ["exclude title ~ /sponsored/i"],
  "feeds": [{"url": "https://go.dev/blog/feed.atom", "name": "The Go Blog"}]
}
```

## Endpoints

| Endpoint | Content type | Description |
| --- | --- | --- |
| `GET /feed.xml` | `application/rss+xml` | Merged feed as RSS 2.0 |
| `GET /feed.rss` | `application/rss+xml` | Alias for `/feed.xml` |
| `GET /feed.atom` | `application/atom+xml` | Merged feed as Atom 1.0 |
| `GET /feed.json` | `application/feed+json` | Merged feed as JSON Feed 1.1 |
| `GET /healthz` | `application/json` | Per-source fetch status |
| `GET /` | `text/plain` | Endpoint listing |

`HEAD` works on every feed endpoint. Anything other than `GET`/`HEAD` gets a
`405` with an `Allow` header.

Every feed response carries `ETag`, `Last-Modified`, `Cache-Control` and an
`X-Feed-Entries` count. Conditional requests are honoured: `If-None-Match`
(including `*`, comma-separated lists and `W/` weak tags) takes priority over
`If-Modified-Since`, exactly as RFC 9110 requires, and a match produces a bodiless
`304`. Each representation gets its own entity tag, so a client caching
`/feed.xml` will not be told its copy of `/feed.json` is fresh.

`/healthz` returns `200` once a merge has succeeded, and `503` while starting up
or when every upstream is failing. `status` is `ok`, `degraded` (some sources
failed), `starting` or `error`:

```json
{
  "status": "degraded",
  "uptime_sec": 512,
  "entries": 137,
  "failures": 1,
  "last_update": "2026-07-27T12:00:00Z",
  "sources": [
    {"url": "https://go.dev/blog/feed.atom", "name": "The Go Blog",
     "ok": true, "not_modified": true, "status": 304, "entries": 24, "duration_ms": 41},
    {"url": "https://down.example/feed.xml",
     "ok": false, "not_modified": false, "status": 502, "entries": 0,
     "error": "fetch https://down.example/feed.xml: unexpected status 502 Bad Gateway",
     "duration_ms": 12}
  ]
}
```

## Filter DSL

Filters are applied after deduplication, one rule per line:

```
<include|exclude> <field> <~|!~> <pattern>
```

- **Actions:** `include` keeps matching entries, `exclude` drops them.
- **Fields:** `title`, `content`, `summary`, `link`, `author`, `category`,
  `source` (the source name and URL), `any` (all of the above joined).
- **Operators:** `~` (or `=~`) means the regular expression matches; `!~` means
  it does not.
- **Pattern:** the rest of the line, a [Go regular expression](https://pkg.go.dev/regexp/syntax).
  It may be wrapped in slashes with optional flags: `/golang/i` is the same as
  `(?i)golang`. Supported flags are `i` (case insensitive) and `s` (`.` matches
  newline).

Blank lines and lines starting with `#` are ignored. Syntax errors name the
offending line number and refuse to start the server.

**Evaluation order:** an entry is rejected if *any* `exclude` rule matches it.
If at least one `include` rule is present, the entry must additionally match at
least one of them. A rule set containing only `exclude` rules is therefore a
blocklist, and one containing `include` rules is an allowlist that `exclude`
rules can still veto.

```yaml
filters:
  # Blocklist: everything except ads and one noisy author.
  - exclude title ~ /\b(sponsored|advertisement|promoted)\b/i
  - exclude author ~ ^Marketing Team$

  # Allowlist: of what remains, keep only what is on topic.
  - include any ~ /(golang|kubernetes|postgres)/i

  # Keep entries that are *not* release-candidate churn.
  - include title !~ /-rc[0-9]+\b/
```

## Deduplication strategy

Entries from all feeds go into one list and are compared in three stages. The
first stage that matches wins, and the first occurrence of a duplicate group is
the one kept - so ordering feeds by preference in the config decides which copy
survives.

1. **GUID / Atom id.** An exact match on the entry identity. This catches the
   same entry served by a feed and its mirror.
2. **Normalized URL.** Links are canonicalized before comparison: scheme and
   host lowercased, `http` upgraded to `https`, a leading `www.` and default
   ports dropped, tracking parameters (`utm_*`, `fbclid`, `gclid`, `mc_cid`,
   `ref`, ...) removed, remaining query parameters sorted, the fragment dropped
   and a trailing slash trimmed. `http://www.example.com/post/?utm_source=rss`
   and `https://example.com/post` are the same document.
3. **Title similarity.** Titles are stripped of markup and entities, lowercased
   and reduced to alphanumeric words; the Jaccard similarity of the two word
   sets is compared against `title_threshold`. A match also has to fall inside
   `title_window` of publication time, so a yearly "Release notes" post is not
   folded into last year's.

When two entries merge, the survivor is filled in from the other wherever it is
missing a link, body, summary, author, date or categories, and a hash-derived
identity is replaced by a real GUID if the duplicate has one.

Merged entries are sorted newest first (entries with no usable date sort last)
and truncated to `max_items`.

## Parsing details

Written against what feeds actually contain, not only what the specs say:

- **One code path for RSS 2.0 and Atom 1.0.** The document root decides the
  parser; both produce the same normalized entry type. RSS `content:encoded`,
  `dc:creator` and `dc:date` are understood, as are Atom text constructs of type
  `text`, `html` and `xhtml`.
- **Dates.** RFC1123, RFC1123Z, RFC822, RFC822Z, RFC3339 and RFC3339 with
  nanoseconds, plus the malformed variants that are common in the wild: single
  digit days, missing weekdays, missing seconds, missing time zones, a space
  instead of the `T` separator, `2006-01-02` alone, `01/02/2006`, ANSI C form,
  trailing `(UTC)` annotations, the legal-but-unsupported-by-Go `UT` zone, and
  named zones (`EST`, `PDT`, `CEST`, `JST`, ...) whose offsets Go's parser would
  otherwise flatten to zero. A date that still cannot be decoded leaves the
  timestamp empty and is preserved verbatim, visible in `feedmerge fetch`.
- **CDATA and entities.** CDATA sections are unwrapped. HTML entity names
  (`&nbsp;`, `&eacute;`) are accepted, which strict XML would reject, and titles
  escaped twice by the publisher (`AT&amp;amp;T`) are decoded to what the
  publisher meant. Entry bodies keep their markup verbatim.
- **Relative URLs** are resolved against `xml:base` where present, otherwise the
  feed's own link, otherwise the URL the document was fetched from.
- **Identity fallback chain.** `guid`/`id` if present; otherwise the entry link;
  otherwise a SHA-256 hash of the title and raw publication date, emitted as
  `urn:feedmerge:<hex>`. An RSS `guid` that is a permalink is also used as the
  entry link when the item has none. How the identity was derived is reported by
  `feedmerge fetch`.
- **Legacy encodings.** `iso-8859-1` and `windows-1252` documents are decoded; a
  byte-order mark is stripped. Any other declared charset is a clear error rather
  than mojibake.
- **Bounded input.** Response bodies are capped at 16 MiB.

## Concurrency and fetching

Upstreams are fetched by a bounded worker pool (`workers`), each request under a
context deadline (`timeout`). A per-host limiter serializes requests to the same
host and keeps at least `host_interval` between them, so a config full of feeds
from one publisher stays polite.

A feed that times out, returns an error status or fails to parse produces an
error in `/healthz` and is skipped; the merge proceeds with whatever else
succeeded. The refresh only fails outright when *every* source failed, and in
that case the previous good snapshot keeps being served.

Outbound requests are conditional: the `ETag` and `Last-Modified` of the last
successful fetch are sent back as `If-None-Match` and `If-Modified-Since`, and a
`304` reuses the cached parse without re-parsing the document.

## Development

```sh
go build ./...
go vet ./...
go test ./...
go test -race -cover ./...
```

## License

Released under the MIT License. See [LICENSE](LICENSE).

Copyright (c) 2026 roxiproject
