# distconf

`distconf` is the operator-facing CLI for [Elephant
distribution](https://github.com/ttab/elephant-distribution). It keeps a
running distribution service's configuration in sync with a source-controlled
directory of HCL files, so every change to a production environment comes from
a reviewed commit rather than a hand-made RPC call.

The repository is both a library (`github.com/ttab/distconf` — config parsing,
plan building, diffing) and the `distconf` binary (`cmd/distconf`).

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

* **`schema_set`** blocks point at named revisor schemas and the version
  expected for each:

  ```hcl
  schema_set "public" {
    version    = "v0.0.4"
    repository = "https://github.com/ttab/dist-revisorschemas.git"
    schemas    = ["se.ecms.dist", "se.ecms.dist.planning", "se.tt.dist"]
  }
  ```

* **`document`** blocks describe per-type configuration: a transform script
  (either inline via `transform_script` or from a file via `transform_file` —
  the two are mutually exclusive), plus the type's `bounded_collection` and
  `variants` settings.

  ```hcl
  document "core/article" {
    transform_file = "article.ts"
  }

  document "core/section" {}
  ```

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

`testdata/config-example/` is a complete, working example of such a directory.

## Commands

* **`distconf configure --env <name>`** — sets up OIDC credentials and the
  service base URL for an environment. All server-facing commands then take
  `--env` to select which environment to talk to. Non-interactive use can
  supply `--client-id` / `--client-secret` (or `CLIENT_ID` / `CLIENT_SECRET`)
  instead. The CLI requests the `dist_admin` scope.

* **`distconf update [--dir .]`** — re-resolves the schemas referenced by the
  configuration (fetching them from each schema set's git repository) and
  writes a fresh `schema.lock.json`.

* **`distconf apply [--dir .] [--description "…"]`** — loads the lockfile,
  schemas, transform scripts and rendition configuration, calls
  `GetActiveConfigGeneration` to fetch the server's current state, prints a
  coloured diff, and on confirmation calls `RegisterConfigGeneration` with
  `activate = true`. The output reports the new generation's ID.

* **`distconf sync start|stop|status`** — resumes, pauses, or inspects the
  distribution service's sync worker. `status` prints the desired state, the
  state the worker reports, its position in the repository eventlog, and
  whether it has caught up.

* **`distconf version`** — prints the binary version.

## How `apply` works

Distribution-time configuration is versioned server-side in atomic snapshots
called **config generations**. A generation pins a set of schemas (each
`(name, version)`) and a set of per-type configurations, and exactly one
generation is active at a time. A single `apply` run turns the configuration
directory into exactly one new generation, so rolling back is a matter of
activating the previous generation by ID — the old schemas and type configs
are all still stored.

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

`apply` refuses to build a plan for a `document` block whose type isn't
declared by any schema in the set, which catches typos and types that were
dropped from a schema upgrade.

It also **warns** when a rendition source matches a block type for which no
schema in the generation declares a `rel:"rendition"` link. Delivered
documents would then carry rendition links the consumer's copy of the schemas
rejects — the configuration is accepted, but the warning is worth acting on.

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

`ReadConfigFromDirectory` parses a configuration directory, and `BuildPlan`
diffs it against a server's active generation and returns the payload that
would replace it:

```go
conf, err := distconf.ReadConfigFromDirectory(dir)
plan, err := distconf.BuildPlan(ctx, clients, conf, schemas, description)
gen, err := plan.Execute(ctx, clients)
```

`WithSchemasDir` loads the schemas named by each `schema_set` from a local
directory (`{dir}/{name}.json`) instead of fetching them from the configured
repository, which is useful when developing schemas locally or in tests that
must not touch the network.

## Development

```
go test ./...
golangci-lint run
```
