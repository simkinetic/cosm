# cosm reference

A complete description of the cosm interface: the command-line API, the on-disk
file formats, the depot layout, and the extension protocol. For a guided
introduction, start with the [Lua](tutorial-lua.md) or
[C++/CMake](tutorial-cpp.md) tutorial.

- [Global usage](#global-usage)
- [Exit codes](#exit-codes)
- [Commands](#commands)
- [On-disk formats](#on-disk-formats)
- [Depot layout](#depot-layout)
- [Extension protocol](#extension-protocol)

---

## Global usage

```
cosm [command] [args] [flags]
```

Global flags (available on every command):

| Flag | Meaning |
|------|---------|
| `--depot <path>` | Use a specific depot instead of the resolved default. |
| `--version` | Print the version and exit (root command only). |
| `-h, --help` | Help for any command. |

**Depot resolution** (where cosm keeps its state): `--depot` flag → `$COSM_DEPOT`
env → the depot recorded in `${XDG_CONFIG_HOME:-~/.config}/cosm/config.json` →
`~/.cosm`. Run `cosm setup` once to create it.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | success |
| `1` | generic failure |
| `2` | usage error (bad args/flags) |
| `3` | not found (registry, package, or extension) |
| `4` | network error |
| `5` | integrity error (content-hash mismatch or moved tag) |
| `6` | build failure (an extension's build failed) |
| `70` | internal error |

---

## Commands

### Depot

**`cosm setup`** — create the depot structure and `config.json`. Idempotent. Prints
the `export COSM_DEPOT=…` line to add to your shell profile (it never edits your
profile itself).
- `--store <dir>` — persist a default artifact store for `cosm publish`.

### Project

**`cosm init <name> [v<version>]`** — create `cosm.json` in the current directory
(default version `v0.1.0`).
- `--build <ext>` — the build-system extension id (e.g. `lua`, `cmake`).

**`cosm status`** — resolve (offline) and print the project, its direct
dependencies, and the resolved build list, with `(develop)` markers and any
v0-minor-bump warnings.

**`cosm add <name> [v<version>]`** — add a dependency. Looks the package up in the
local registries (prompting if it is found in more than one); with no version it
takes the latest. Writes to `cosm.json` only — no network mutation.

**`cosm rm <name>`** — remove a dependency (prompts if the name spans multiple
majors).

**`cosm upgrade <name> [v<constraint>]`** — raise a dependency's floor within its
major. A partial constraint (`v1`, `v1.2`) narrows the choice.
- `--latest` — newest version in the major (ignore a finer constraint).
- `--all` — upgrade every direct dependency.

**`cosm downgrade <name> v<version>`** — lower a dependency to an earlier version
using the MVS downgrade cascade: any dependency that pins it higher is downgraded
to its highest still-compatible version (each such change is reported); a
genuinely unsatisfiable dependency is an error.

**`cosm release [v<version>]`** — publish a release of the current package: require
a clean, in-sync worktree, set the version in `cosm.json`, commit, tag, and push
the branch + tag. It only touches the package's own git repo (registration is a
separate step).
- `--patch` / `--minor` / `--major` — bump instead of naming a version.

### Build & run

All of these accept `--release` (default), `--debug`, and `--jobs <n>`.

**`cosm build`** — resolve → materialize (fetch/verify or download binaries) →
build the dependencies and the project in topological order, reusing the
build-key artifact cache.

**`cosm run [--] <command> [args…]`** — build if needed, then run `<command>` with
the assembled environment. The command is resolved against that environment's
`PATH`, so a just-built binary is found. E.g. `cosm run -- lua src/main.lua`,
`cosm run -- ./app`.

**`cosm test`** — build, then invoke the project extension's `test` verb.

**`cosm env`** — print the assembled environment as shell `export` lines; load it
with `eval "$(cosm env)"`.

**`cosm shell`** (alias **`cosm activate`**) — build, then open an interactive
shell (`$SHELL`) with the environment applied.

### Develop

**`cosm develop <name>`** — check a dependency out into the depot's shared
workspace (`$COSM_DEPOT/dev/<name>@v<major>`) and enroll this project to use it.
Enrollment is authoritative; the checkout is created on demand.
- `--major <n>` — disambiguate which major to develop.
- `--branch <b>` / `--tag <v>` — check out a branch or a released tag (default: the
  repo's default branch, so you can commit).

**`cosm free <name>`** — stop developing a dependency for this project.
- `--major <n>` — disambiguate which major to free.
- `--purge` — also delete the shared checkout.

### Registry

**`cosm registry init <name> <giturl>`** — create a registry from an empty remote.
- `--kind source|binary|mixed` — registry kind (default `source`).

**`cosm registry clone <giturl>`** — clone an existing registry into the depot.

**`cosm registry add <name> <giturl>`** — register a package and its released
versions. Idempotent: on an already-registered package it picks up any new tags.

**`cosm registry rm <name> <pkg> [v<version>]`** — remove a package (or a single
version) from a registry.
- `-f, --force` — skip the confirmation prompt.

**`cosm registry status <name>`** — list a registry's packages and their versions.

**`cosm registry delete <name>`** — delete a registry from the local depot.
- `-f, --force` — skip the confirmation prompt.

**`cosm registry sync <name>`** — scan every registered package's remote for new
semver tags and register any that are missing (one atomic commit + push). Intended
to run server-side on a schedule; see [registry CI](registry-ci.md).

**`cosm update [registry]`** — sync registries with their remotes (a read-only
`git pull`). With no argument it updates every registry; with a name, just that
one.

### Binaries

**`cosm publish --registry <r> [--store <dir>]`** — build the current package for
the local platform, upload the artifact to the store, and record a binary in a
`binary`/`mixed` registry (§8.5). `--store` defaults to `artifactStore` in
`config.json` (set via `cosm setup --store`).
- `--registry <r>` — target binary/mixed registry (required).
- `--store <dir>` — artifact store directory (or from config).
- `--debug` — debug build.

---

## On-disk formats

All documents are JSON with a `schemaVersion` (current: `1`). Unknown fields are
preserved on rewrite.

### `cosm.json` — project/package manifest

Committed to the package's git repo.

```json
{
  "schemaVersion": 1,
  "name": "linalg",
  "uuid": "66c27472-84bc-4dc4-b320-430f08b0b9fb",
  "version": "v0.1.0",
  "authors": [ { "name": "…", "email": "…" } ],
  "build": "cmake",
  "provides": ["linalg@v0"],
  "deps": {
    "<dep-uuid>@v0": { "name": "terrastd", "version": "v0.6.4" }
  },
  "ext": { "cmake": { "minimumVersion": "3.24" } }
}
```

- `build` — the extension id that owns building/activation.
- `provides` — module namespace(s) exposed, each `<namespace>@v<major>`.
- `deps` — direct requirements only; keyed by the **compatibility unit**
  `<uuid>@v<major>`; the `version` is the minimum (MVS floor).
- `ext` — extension-specific config, opaque to the core.

### `registry.json` — registry index

At a registry repo's root.

```json
{
  "schemaVersion": 1,
  "name": "TerraStandard",
  "uuid": "…",
  "giturl": "git@github.com:org/TerraStandard.git",
  "kind": "source",
  "packages": { "terrastd": { "uuid": "…", "giturl": "git@github.com:org/terrastd.git" } }
}
```

Per-version metadata lives at `<registry>/<SHARD>/<pkg>/<version>/specs.json`,
where `<SHARD>` is the uppercase first letter of the package name; the version
list is `<registry>/<SHARD>/<pkg>/versions.json`.

### `specs.json` — immutable per-version metadata

```json
{
  "schemaVersion": 1,
  "name": "terrastd", "uuid": "…", "version": "v0.6.4",
  "giturl": "git@github.com:org/terrastd.git",
  "commit": "6c6f7e…", "tree": "sha256:…",
  "build": "cmake", "provides": ["std@v0"],
  "deps": { "<dep-uuid>@v1": { "name": "terratest", "version": "v1.0.1" } },
  "binaries": [
    {
      "platform": { "os": "linux", "arch": "amd64" },
      "toolchain": "gcc-13-libstdc++",
      "config": { "buildType": "Release" },
      "buildKey": "sha256:…",
      "deps": [ { "uuid": "…", "version": "v0.6.4", "buildKey": "sha256:…" } ],
      "artifact": { "url": "https://…/linalg-linux-amd64.tar.gz", "hash": "sha256:…", "size": 12345 },
      "descriptor": { "cmakePrefixPath": ["."], "libDirs": ["lib"] }
    }
  ]
}
```

- `commit` — the immutable identifier; present even in binary-only registries.
- `tree` — sha256 of the exported source tree; the integrity anchor and a
  build-key input.
- `giturl` — required in `source`/`mixed`; may be omitted in a `binary`-only
  registry to withhold source.
- `binaries` — prebuilt artifacts (binary/mixed registries only); each addressed
  by URL + hash, matched by `buildKey`, with a relocatable consumption descriptor.

### `config.json` — depot config (`$COSM_DEPOT/config.json`)

```json
{ "schemaVersion": 1, "depot": "/Users/you/.cosm", "defaultShell": "bash", "artifactStore": "/Users/you/artifacts" }
```

### Local/develop state

- `$COSM_DEPOT/dev/workspace.json` — shared develop checkouts: `[{name, uuid,
  major, giturl, ref, refKind, path}]`.
- `<project>/.cosm/develop.json` — a project's enrollment: `{ "enrolled":
  ["<uuid>@v0", …] }`. Git-ignored; never committed.

---

## Depot layout

```
$COSM_DEPOT/
  config.json                       depot config
  registries/registries.json        [{name, uuid, giturl}]
  registries/<name>/                a git clone of each registry
  mirrors/<uuid>.git/               bare fetch cache per package
  sources/<uuid>/<commit>/          verified, immutable source tree
  builds/<uuid>/<commit>/<key>/     built artifacts + descriptor.json + meta.json
  cache/buildlists/<uuid>/<commit>.json   memoized MVS closures
  extensions/<id>/cosm-ext-<id>     depot-installed extensions
  dev/workspace.json, dev/<name>@vN/   co-development workspace
  logs/                             build/op logs
```

---

## Extension protocol

Extensions are executables the core invokes; the core stays language-agnostic.

**Discovery.** For build id `X`, cosm runs `cosm-ext-X`, found in
`$COSM_DEPOT/extensions/X/cosm-ext-X` (preferred) or on `PATH`.

**Invocation.** `cosm-ext-X <verb>` with a JSON request on **stdin** and a JSON
response on **stdout**; logs go to **stderr**; exit 0 means success. Protocol
version: `1`.

**Verbs:**

| Verb | Purpose |
|------|---------|
| `info` | Report identity, toolchain id, and capabilities. |
| `scaffold` | Create a new package skeleton (for templates). |
| `build` | Configure/compile/install a package into a prefix; emit a descriptor. |
| `test` | (optional) Run the package's tests in the built environment. |
| `activate` | Produce the run/test environment for a project + its deps. |

**`info` response:**
```json
{ "extension": "cmake", "version": "0.1.0", "protocol": 1,
  "languages": ["c++","c"], "toolchainId": "clang-17-darwin-arm64",
  "capabilities": ["info","build","activate","scaffold","test"] }
```

**`build` request → response:**
```json
{ "package": { "name": "…", "uuid": "…", "version": "…", "source": "/…/sources/…",
               "provides": ["…"], "ext": { } },
  "prefix": "/…/builds/…/artifacts", "buildKey": "sha256:…",
  "platform": { "os": "darwin", "arch": "arm64" },
  "config": { "buildType": "Release" },
  "deps": [ { "name": "…", "uuid": "…", "version": "…", "prefix": "/…", "descriptor": { } } ],
  "jobs": 8 }
```
```json
{ "status": "ok", "descriptor": { }, "artifacts": ["…"], "log": "/…/log" }
```

**`activate` request → response:**
```json
{ "project": { "name": "…", "source": "/project/dir", "provides": ["…"] },
  "platform": { "os": "darwin", "arch": "arm64" },
  "deps": [ { "name": "…", "prefix": "/…", "descriptor": { } } ] }
```
```json
{ "env": { "LUA_PATH": "…" },
  "prependPaths": { "CMAKE_PREFIX_PATH": ["/…"], "DYLD_LIBRARY_PATH": ["/…/lib"] } }
```
The core applies `env` verbatim and **prepends** `prependPaths` (with the OS path
separator) to the inherited values.

**Consumption descriptors are opaque to the core and must be relocatable:** they
use paths **relative to the install prefix**, and the core supplies each
dependency's actual `prefix` in `DepCtx`. This is what lets a binary artifact,
built elsewhere, work in a consumer's depot.

**Reference extensions:** `cosm-ext-lua` (interpreted; `LUA_PATH` assembly) and
`cosm-ext-cmake` (CMake configure/build/install with `find_package` wiring).
