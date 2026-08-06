# distconf

`distconf` is the operator-facing CLI for configuring Elephant services. It
keeps a running service's configuration in sync with a source-controlled
directory of HCL files, so every change to a production environment comes from
a reviewed commit rather than a hand-made RPC call.

Two services are supported so far: [Elephant
distribution](https://github.com/ttab/elephant-distribution) and [Elephant
live](https://github.com/ttab/elephant-live).

The repository is both a library (`github.com/ttab/distconf` — shared config
parsing and schema plan machinery, with service-specific packages
`distconf/distribution` and `distconf/live`) and the `distconf` binary
(`cmd/distconf`).

## Installation

Binaries for Linux, macOS and Windows are attached to each
[release](https://github.com/ttab/distconf/releases). To build from source:

```
go install github.com/ttab/distconf/cmd/distconf@latest
```

## Configuration directory

A configuration directory contains any number of `.hcl` files, all merged into
one configuration, plus a `schema.lock.json` lockfile pinning the content hash
of every schema currently committed. Splitting blocks across files is purely
organisational — `document` blocks can live in `article.hcl` and
`taxonomy.hcl`, and both are read.

Every configuration directory identifies the service it configures with a
**`configuration`** block, declared exactly once (conventionally in
`main.hcl`):

```hcl
configuration {
  service = "distribution"
  version = 1
}
```

`service` selects which service `apply` talks to and which blocks are valid in
the directory; `version` is the configuration format version (currently `1`).

**`schema_set`** blocks are common to all services. They point at named
revisor schemas and the version expected for each:

```hcl
schema_set "public" {
  version    = "v0.0.4"
  repository = "https://github.com/ttab/dist-revisorschemas.git"
  schemas    = ["se.ecms.dist", "se.ecms.dist.planning", "se.tt.dist"]
}
```

### Distribution (`service = "distribution"`)

* **`document`** blocks describe per-type configuration: a transform script
  (either inline via `transform_script` or from a file via `transform_file` —
  the two are mutually exclusive), plus the type's `bounded_collection`,
  `variants`, `embeddings` and `anchor` settings.

  ```hcl
  document "core/article" {
    transform_file = "article.ts"
    embeddings     = true
  }

  document "core/section" {}
  ```

  `embeddings` turns on semantic indexing for the type: its documents are
  chunked and embedded, and it can be searched and subscribed to by vector.
  It needs a deployment with an embedding sidecar, and it only takes effect
  for indexes created after it is applied — the vector field cannot be added
  to an index that already exists, so switching it on for a type that is
  already indexed takes a new index generation.

  `anchor` decides how the type is partitioned across the search indexes,
  and nothing else — it is independent of `embeddings`. Leave it out for a
  non-temporal type (one unpartitioned index per language, never archived);
  `first_published` partitions news content by when it was first
  distributed, which is immutable, so a document never moves between
  partitions; `time_expressions` partitions by dates read out of the
  document itself, for content that is *about* a date rather than published
  on one, and everything from the current quarter onwards shares one index.

  ```hcl
  document "core/planning-item" {
    anchor = "time_expressions"

    time_expression {
      expression = ".meta(type='core/planning-item').data{start_date:date}"
    }
  }
  ```

  `time_expression` blocks are newsdoc value-extractor expressions, with an
  optional `layout` and `timezone`. The `time_expressions` anchor requires at
  least one and every other anchor refuses them, because both mismatches are
  otherwise silent: a forward-anchored type with no expressions falls back to
  anchoring on first-published, and expressions under any other anchor are
  read by nothing.

  **Set `timezone` only for wall-clock timestamps that carry no offset**, the
  `"2006-01-02 15:04"` kind of value. Leave it off for a date-only value:
  partitions are cut in UTC, so reading a bare date as UTC keeps it on the
  day it says, while reading it in a zone east of UTC moves it back a day —
  and on a quarter boundary, back a whole quarter.

  Like `embeddings`, `anchor` only shapes indexes that don't exist yet, so
  changing it for a type that is already indexed takes a new index
  generation.

  `facet` blocks declare the values the daily views —
  `Content.ListPublishedVersions` and `Content.ListPlannedVersions` — can
  narrow on. The label is the facet name a request filters by, the
  expression is a newsdoc value extractor, and several blocks may share a
  name so their values are unioned.

  ```hcl
  document "core/article" {
    facet "section" {
      expression = ".links(rel='section')@{uuid}"
    }
  }
  ```

  Facets are extracted **when a version is stored**, not at query time, so
  two things follow. A facet only narrows content published after it was
  applied — adding one to a type that already has content means backfilling
  the rest. And the narrowing is per version: an article that was in Sport
  at 08:00 and moved out in v3 is still in Sport's published day at 08:00,
  which is what a publication log should say.

  **The values should be document UUIDs, not labels.** A facet filter
  matches exactly, with no analysis or case folding, so `section = "sport"`
  matches nothing at all. A section is a `core/section` document; the
  client filters by the UUIDs it already holds and resolves display names
  itself. An expression is needed per type because a section is not in the
  same place in every one of them.

* **`renditions`** blocks — one per asset kind — declare the delivery-time
  rendition configuration: `default_variants`, `default_extension`, and
  ordered `source` blocks that match asset references by block type, link rel
  and link type, and an anchored `uri_pattern` with exactly one capture group
  extracting the asset ID. Sources are evaluated in order; the first match
  wins. For the `image` kind `block_types` and `link_rel` are optional,
  defaulting to `core/image`/`image`. A kind may only be declared once across
  the whole directory.

  ```hcl
  renditions "image" {
    default_variants  = ["thumbnail", "preview", "hires"]
    default_extension = "jpg"

    source "tt-archive" {
      namespace   = "mm"
      link_types  = ["tt/picture", "tt/graphic"]
      uri_pattern = "^https?://tt\\.se/media/image/sdl([A-Za-z0-9._-]+)$"
    }
  }
  ```

`distribution/testdata/config-example/` is a complete, working example of
such a directory.

### Live (`service = "live"`)

* **`post_type`** blocks declare the document types the live service accepts
  as post content. Every type must be declared by one of the schemas, and a
  type may only be declared once across the whole directory.

  ```hcl
  post_type "core/live-post" {}
  ```

`live/testdata/config-example/` is a complete, working example.

## Commands

* **`distconf configure --env <name>`** — sets up OIDC credentials and the
  service base URLs for an environment. All server-facing commands then take
  `--env` to select which environment to talk to. Non-interactive use can
  supply `--client-id` / `--client-secret` (or `CLIENT_ID` / `CLIENT_SECRET`)
  instead. The CLI requests the `dist_admin` scope for distribution commands
  and `liveblog_admin` for live commands.

* **`distconf update [--dir .]`** — re-resolves the schemas referenced by the
  configuration (fetching them from each schema set's git repository) and
  writes a fresh `schema.lock.json`.

* **`distconf apply [--dir .] [--description "…"]`** — loads the lockfile,
  schemas and service configuration, calls `GetActiveConfigGeneration` on the
  service named by the `configuration` block to fetch its current state,
  prints a coloured diff, and on confirmation calls
  `RegisterConfigGeneration` with `activate = true`. The output reports the
  new generation's ID.

* **`distconf distribution sync start|stop|status`** — resumes, pauses, or
  inspects the distribution service's sync worker. `status` prints the
  desired state, the state the worker reports, its position in the repository
  eventlog, and whether it has caught up.

* **`distconf distribution generation list|create|wait|activate|delete`** —
  manages the distribution service's *index* generations: one complete set of
  search indexes, under one prefix, on one cluster, with an eventlog cursor of
  its own. This is how the index is rebuilt from scratch after a mapping
  change that cannot be applied in place, how an OpenSearch upgrade is done,
  and how a lost cluster is recovered from.

  `list` is the catch-up view — every generation with the eventlog position
  its indexer has reached, its lag, and the head of the log — and takes
  `--json`. `create` registers a generation and starts building it in the
  background while the active one keeps serving; it is additive and changes
  nothing about what is being delivered. `wait` polls until the generation is
  within `--max-lag` (10, matching the service's activation gate) of the head.
  `activate` switches search, calibration and subscription matching over, and
  prints the position subscription matching was handed over at. `delete`
  removes an inactive generation, its indexes and its snapshots.

  A generation reports position 0 and full lag while it drains the archive,
  which is the first phase of every rebuild and can take a long time. That is
  not a stall — the indexer stores no position until it starts tailing the
  log.

* **`distconf version`** — prints the binary version.

## How `apply` works

Service configuration is versioned server-side in atomic snapshots called
**config generations**. A generation pins a set of schemas (each
`(name, version)`) plus the service-specific configuration (per-type
configurations and renditions for distribution, post types for live), and
exactly one generation is active at a time. A single `apply` run turns the
configuration directory into exactly one new generation, so rolling back is a
matter of activating the previous generation by ID — the old schemas and
configuration are all still stored.

The diff `apply` prints is display-only; nothing is mutated until you confirm,
and then the whole generation is registered and activated in one call:

```
+ add schema se.tt.dist@v0.0.4
~ upgrade schema se.ecms.dist v0.0.3 => v0.0.4
~ update transform script for "core/article":
  --- current
  +++ wanted
  …
+ configure "image" renditions:
  { … }
- remove type configuration for "core/author"
```

`apply` refuses to build a plan for a `document` or `post_type` block whose
type isn't declared by any schema in the set, which catches typos and types
that were dropped from a schema upgrade.

For distribution it also **warns** when a rendition source matches a block
type for which no schema in the generation declares a `rel:"rendition"` link.
Delivered documents would then carry rendition links the consumer's copy of
the schemas rejects — the configuration is accepted, but the warning is worth
acting on.

## Changing a schema

Schema identity is `(name, version)` and stored specs are **immutable**
server-side. Editing a schema's content therefore requires **bumping its
version**: re-tagging the same version with different content will not take
effect, because re-registering an existing `(name, version)` with a changed
spec is rejected and any instance that already stored the old spec keeps
serving it. The flow is:

1. Edit and tag a new release of the schema repository.
2. Bump `version` in the `schema_set` block.
3. `distconf update` to re-resolve and rewrite `schema.lock.json`.
4. `distconf apply` to roll and activate a new generation.

> **Operator note.** revisor's prune step removes anything the active schemas
> don't declare, and only errors when a document can't be pruned to a valid
> state at all. A schema/content mismatch therefore shows up as **blocks
> quietly dropped from distributed documents**, never as a failed ingest. If
> images or other blocks are disappearing downstream, suspect the active
> schema set first.

## Library use

The service packages parse configuration directories and build plans;
`BuildPlan` diffs a configuration against a server's active generation and
returns the payload that would replace it:

```go
import "github.com/ttab/distconf/distribution"

conf, err := distribution.ReadConfigFromDirectory(dir)
plan, err := distribution.BuildPlan(ctx, clients, conf, schemas, description)
gen, err := plan.Execute(ctx, clients)
```

The `live` package mirrors the same API for the live service.

`WithSchemasDir` loads the schemas named by each `schema_set` from a local
directory (`{dir}/{name}.json`) instead of fetching them from the configured
repository, which is useful when developing schemas locally or in tests that
must not touch the network.

The root `distconf` package holds what is shared between services: the
`configuration` block, schema set loading and lockfile handling, and the
schema part of plan building.

## Development

```
go test ./...
golangci-lint run
```
