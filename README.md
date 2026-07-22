# cosm

A language-agnostic package manager built on **Minimal Version Selection** (MVS),
with an integrated git-based registry and out-of-process build-system extensions.
This is the vNext implementation of the design in [docs/SPEC.md](docs/SPEC.md).

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

## Documentation

- **[Reference](docs/reference.md)** — the complete API: every command and flag,
  the on-disk formats, the depot layout, and the extension protocol.
- **[Lua tutorial](docs/tutorial-lua.md)** — publish a library, test it, consume it,
  build, run, and co-develop. *(Also an integration test, so it stays correct.)*
- **[C++ / CMake tutorial](docs/tutorial-cpp.md)** — a compiled library + app with
  `find_package`, `cosm test` with test-only deps, plus optional binary distribution.
  *(Also an integration test.)*
- **[Registry CI](docs/registry-ci.md)** — keeping a registry up to date
  server-side with a scheduled `cosm registry sync`.
- **[Develop-workspace agent guide](docs/dev-workspace.md)** — how to drive cosm
  from Claude Code (or another agent) in a co-development workspace. `cosm setup`
  writes this to `$COSM_DEPOT/dev/CLAUDE.md` for you.
- **[SPEC](docs/SPEC.md)** — the full design specification.

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
| Registry | `registry init [--kind]/clone/add/rm/status/delete/sync`, `update` |
| Binaries | `publish` (to a binary/mixed registry) |

See the **[reference](docs/reference.md)** for every command's arguments, flags,
and behavior.

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

## License

cosm is licensed under the [MIT License](LICENSES/MIT.txt). The repository is
[REUSE](https://reuse.software)-compliant: every file carries SPDX copyright and
license information (in-file for source, via `REUSE.toml` for the rest), so
`reuse lint` reports full compliance. Contributions are accepted under the same
license.
