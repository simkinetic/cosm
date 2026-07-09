# cosm

A language-agnostic package manager built on **Minimal Version Selection** (MVS),
with an integrated git-based registry and out-of-process build-system extensions.
This is the vNext implementation of the design in [SPEC.md](../cosm/SPEC.md).

## Highlights

- **MVS resolution** — 100% reproducible version selection with no lockfile; the
  registry stores only immutable per-version specs, and build lists are computed
  on demand (canonical MVS, `internal/resolve`).
- **Language-agnostic core** — all language/build knowledge lives in out-of-process
  **extensions** invoked over a small JSON protocol (`internal/ext`). Two reference
  extensions ship: `cosm-ext-lua` and `cosm-ext-cmake`.
- **Content-verified materialization** — sources are exported by commit and verified
  against a recorded tree hash; artifacts are cached by a DAG-aware build key.
- **git-based registries** — decentralized; public and private use the same
  interface, with SSH the default transport.
- **Source & binary registries** — publish prebuilt artifacts to a binary/mixed
  registry (`cosm publish`) and consume them with no source access; access is
  gated by registry permissions (clearance tiers, §8.5).
- **Co-development** — shared develop checkouts with per-project opt-in.

## Build

```sh
make build extensions      # produces ./cosm, ./cosm-ext-lua, ./cosm-ext-cmake
```

Put all three on your `PATH` (or install extensions under
`$COSM_DEPOT/extensions/<id>/cosm-ext-<id>`).

## Tutorials

Step-by-step, end-to-end walkthroughs (each is also an integration test, so the
commands are verified to work):

- [Lua tutorial](docs/tutorial-lua.md) — publish a library, consume it, build,
  run, and co-develop.
- [C++ / CMake tutorial](docs/tutorial-cpp.md) — a compiled library + app with
  `find_package`, plus optional binary distribution.

## Quick start

```sh
cosm setup                                   # initialize the depot (~/.cosm)
cosm registry init myreg git@host:myreg.git  # create a registry (empty remote)

# publish a package
cosm init mypkg --build lua                  # write cosm.json
# ... commit + push to a remote ...
cosm release --patch                         # tag + push a release
cosm registry add myreg git@host:mypkg.git   # register it

# consume it
cosm init myapp --build lua
cosm add mypkg v0.1.1
cosm status                                  # show the resolved build list
cosm build                                   # build deps + project via the extension
cosm run -- lua src/main.lua                 # run in the assembled environment
```

## Commands

| Area | Commands |
|------|----------|
| Depot | `setup` |
| Project | `init`, `status`, `add`, `rm`, `upgrade`, `downgrade`, `release` |
| Build/run | `build`, `run`, `test`, `env`, `shell` |
| Develop | `develop`, `free` |
| Registry | `registry init [--kind]/clone/add/rm/status/update/delete/sync`, `update` |
| Binaries | `publish` (to a binary/mixed registry) |

Registries are maintained **server-side**: `cosm update` (clients) is a read-only
`git pull`, while a scheduled `cosm registry sync` on the registry discovers and
registers new upstream package tags. See [registry CI](docs/registry-ci.md).

## Architecture

```
internal/
  types        on-disk data model (cosm.json, registry.json, specs.json, ...)
  semver       v-prefixed SemVer 2.0.0 + compatibility-unit keys
  resolve      Minimal Version Selection (pure)
  buildkey     DAG-aware artifact cache key
  treehash     content hash of a source tree (integrity anchor)
  errs         typed error taxonomy + exit codes
  gitx         Git behind an interface
  depot        depot layout, config, lock
  manifest     JSON load/save
  registry     sharded registry layout, disk SpecLoader, mutations
  develop      develop overlay (workspace + per-project enrollment)
  ext          extension protocol + runner
  materialize  fetch/verify sources, topological build, env assembly
  service      project-level operations
  cli          cobra command tree
cmd/
  cosm-ext-lua    reference Lua extension
  cosm-ext-cmake  reference C++/CMake extension
```

## Testing

```sh
make test        # all tests (hermetic; real git via file:// remotes)
make cover       # coverage with a per-package gate on the core engine
```

The pure core (resolver, semver, build key) is exhaustively unit-tested; the CLI
and extensions are covered end-to-end by in-process and real-binary flows.

## Status

Implemented for macOS and Linux: the full lifecycle — init → release → register →
add → resolve → build → run → test → develop/free → upgrade/downgrade, plus
**binary registries** (`publish` → consume with no source access, §8.5).

`downgrade` runs the full MVS downgrade (§7.5): it lowers the target and
cascade-downgrades any dependency that pins it higher to the highest compatible
version, warning on each forced change; only a genuinely unsatisfiable dependency
is an error.

Out of scope for now: Windows.
