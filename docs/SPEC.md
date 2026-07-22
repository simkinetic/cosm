# COSM (vNext) — Specification

> Target audience: an implementing agent. This document is prescriptive. Where it
> says MUST/SHOULD/MAY, use RFC-2119 meaning. On-disk formats, the extension
> protocol, and the resolution algorithm are normative; command help text and
> cosmetic output are illustrative.

## 0. Status of this document

This specifies a **clean-slate rebuild** of the `cosm` package manager in Go, with
new on-disk formats (each carrying a `schemaVersion`). It supersedes the prototype.
There is no requirement for on-disk backward compatibility; a one-off migration is
described in §14.

Initial language/build-system support ships as **two reference extensions**: Lua
(interpreted, path-based) and C++/CMake (compiled, artifact-based). The core
contains **zero** language- or build-system-specific logic.

---

## 1. Goals & non-goals

### Goals
1. **Minimal Version Selection (MVS)** for 100% reproducible resolution without a
   version lockfile.
2. A **language-agnostic core** plus **out-of-process extensions** that add
   language/build-system support. C++/CMake and Lua must both be expressible as
   extensions with no core changes.
3. A **git-like CLI** that is pleasant, scriptable, and offline-first.
4. **Integrity**: content-verified materialization (detect moved tags / tampering),
   without introducing a lockfile.
5. **Clearance-tiered distribution**: support pure-source and pure-binary registries so
   an organization can grant source access or binary-only access per registry, with
   **identical resolution** either way (§8.5).

### Non-goals (vNext)
- Cross-language dependency edges (a C++ package depending on a Lua package). The
  data model does not forbid it, but the reference extensions are not required to
  interoperate. See §15.
- A central hosted registry service. Registries remain git repositories.
- Windows support is desirable but not required for the first cut (design must not
  preclude it; see §15).

---

## 2. Concepts & terminology

- **Package**: a versioned, git-hosted unit of code identified by a stable **UUID**.
- **Project**: a working checkout of a package (has a `cosm.json`). "Project" and
  "package" are the same artifact seen from working-tree vs. registry perspective.
- **Registry**: a git repository serving as a decentralized index mapping package
  names → UUID + git URL, plus immutable per-version metadata. Public and private
  registries use the identical interface.
- **Depot**: the local machine store (`$COSM_DEPOT`). Holds registries, source
  mirrors, materialized sources, build artifacts, caches, extensions, config.
- **Module namespace**: the import-visible name a package *provides*, versioned by
  major (e.g. package `terrastd` provides namespace `std@v0`). This is declared
  (§4.1); it is NOT inferred from directory names.
- **Extension**: an executable implementing the protocol in §9 for one or more
  languages/build systems.
- **Consumption descriptor**: an opaque (to the core) JSON object an extension emits
  after building a package ("how to depend on me") and consumes when building a
  dependent. The core only routes descriptors dep→dependent; it never parses them.
- **Build key**: a content hash identifying a specific built artifact of a version
  for a specific platform/toolchain/config/dep-set (§8.3). Keys the artifact cache.
- **Build list**: the MVS result — the set of exact versions selected for a project.
- **Registry kind**: `source` (packages fetched + built from git), `binary` (packages
  consumed as prebuilt artifacts), or `mixed`. Resolution is identical across kinds;
  only materialization differs (§8.5). Access is gated by git/host permissions.
- **Binary artifact**: a prebuilt install prefix + consumption descriptor for one
  `{version, platform, toolchain, config, dep-set}` (i.e. one build key), referenced by
  URL + content hash and stored in an external artifact store.

---

## 3. Design principles

1. **Registry stores only immutable facts.** Per-version metadata (`specs.json`)
   records direct requirements plus integrity hashes. The transitive build list is
   **never** stored in the registry (see §7 for why, and for the local cache that
   preserves efficiency).
2. **Pure resolve, explicit sync.** Resolution is offline and side-effect-free over
   the local depot. Network operations (`update`, `release`, `registry add`) are
   explicit and never triggered as a side effect of a read.
3. **Core is language-blind.** All language/build knowledge lives in extensions.
   The core orchestrates: resolve → materialize → build (topological) → activate.
4. **Everything content-addressed & atomic.** Sources by commit; artifacts by build
   key; registry mutations are single atomic commits with rebase-retry.
5. **Typed errors, stable exit codes** (§10). Library code returns data; the CLI
   renders it.

---

## 4. Data model & on-disk formats

All manifests are JSON with 2-space indentation and a `schemaVersion` integer
(current value `1`). Unknown fields MUST be preserved on rewrite (forward-compat).

### 4.1 Project manifest — `cosm.json`

Lives at a project root. Committed to the package's git repo. (Renamed from the
prototype's `Project.json`.)

```json
{
  "schemaVersion": 1,
  "name": "linalg",
  "uuid": "66c27472-84bc-4dc4-b320-430f08b0b9fb",
  "version": "v0.1.0",
  "authors": [
    { "name": "René Hiemstra", "email": "rene.r.hiemstra@gmail.com" }
  ],
  "build": "cmake",
  "provides": ["linalg@v0"],
  "deps": {
    "d38cb13b-f524-4528-8350-3eaccb42afc5@v0": {
      "name": "terrastd",
      "version": "v0.6.4"
    }
  },
  "ext": {
    "cmake": { "minimumVersion": "3.24", "targets": ["linalg"] }
  }
}
```

Field notes:
- `build` (string): the **extension id** that owns this package's build & activation.
  (Named `build` rather than `language` because it selects a build system; an
  extension MAY handle multiple languages.)
- `provides` (array of strings): module namespace(s) this package exposes, each
  `<namespace>@v<major>`. Semantics are **extension-defined**: import roots for
  path-based languages (Lua), exported package/target names for C++/CMake.

  **Namespace binding rule.** The major is part of the namespace so two majors of a
  package can be consumed simultaneously — the basis for incremental migration
  (§12.2: `cosm add <name> v<N>` → migrate imports → `cosm rm` old major). Each extension
  binds the canonical `<namespace>@v<major>` token to its language's surface. Where
  the language admits `@` in an identifier/path the token is used verbatim (Lua:
  `require("strutil@v0.strutil")` from `src/strutil@v0/`). Where it does not
  (C++/CMake — `@` is illegal in identifiers and CMake target names), the extension
  MUST re-spell it deterministically to a legal form, versioning the surface so the
  majors do not collide: the reference cmake extension maps `greet@v0` →
  `greet_v0`, used as the include prefix, the C++ `namespace`, and the CMake
  package/target (`greet_v0::greet_v0`), so `greet_v0::` and `greet_v1::` link into
  one program. The core never interprets the token; the mapping is the extension's
  contract with its consumers.
- `deps` (object): **direct requirements only**. Key = `<dep-uuid>@v<major>`
  (compatibility unit, §6.3). Value = `{ name, version }` where `version` is the
  declared **minimum** version (MVS floor). `name` locates the package (registry
  shard/path); `uuid` is authoritative for identity and is verified against the
  registry to guard against name collisions across registries.
- `testDeps` (object): test-only requirements, same shape and keying as `deps`.
  Included in resolution only for the package being tested, and **non-transitive**
  (§7.6): never carried into registry specs, so consumers never inherit them. A
  compatibility unit appears in `deps` or `testDeps`, not both.
- `ext` (object): extension-specific configuration, **opaque to the core**, passed
  verbatim to the extension. Keyed by extension id.
- `authors`: structured (replaces the prototype's `[name]email` string encoding).

### 4.2 Registry root — `registry.json`

At the root of a registry git repo. Authoritative package list.

```json
{
  "schemaVersion": 1,
  "name": "TerraStandard",
  "uuid": "4f3f631f-c49b-42d2-874d-458e11d2e62d",
  "giturl": "git@github.com:simkinetic/TerraStandard.git",
  "kind": "source",
  "packages": {
    "terrastd": {
      "uuid": "d38cb13b-f524-4528-8350-3eaccb42afc5",
      "giturl": "git@github.com:simkinetic/terrastd.git"
    }
  }
}
```

### 4.3 Registry per-version layout (sharded)

Kept human-inspectable and git-diff-friendly (decision: sharded, not a single
index). For package `terrastd`:

```
<registry>/
  registry.json
  T/                       # shard = uppercase first letter of package name
    terrastd/
      versions.json        # ["v0.2.0", ..., "v0.6.4"]  (sorted, semver order)
      v0.6.4/
        specs.json         # immutable; see 4.4  (NO buildlist.json here)
```

### 4.4 Version specs — `specs.json` (immutable)

Written once at registration; never mutated (correcting it requires removing and
re-adding the version).

```json
{
  "schemaVersion": 1,
  "name": "terrastd",
  "uuid": "d38cb13b-f524-4528-8350-3eaccb42afc5",
  "version": "v0.6.4",
  "giturl": "git@github.com:simkinetic/terrastd.git",
  "commit": "6c6f7e5f33634ca3e64f870f54d2e1479820028c",
  "tree": "sha256:<hex>",
  "build": "cmake",
  "provides": ["std@v0"],
  "deps": {
    "4510efb2-5399-47b1-a173-7e7c2ca7b288@v1": {
      "name": "terratest",
      "version": "v1.0.1"
    }
  },
  "ext": { "cmake": { "minimumVersion": "3.24" } }
}
```

- `commit`: full git commit SHA the version tag resolved to at registration. Present
  in every spec as the immutable identifier — even in a binary-only registry, where it
  identifies the source the binary was built from without granting source access.
- `tree`: **content hash of the exported source tree** (see §6.4), the integrity
  anchor. `sha256:` prefix reserved for algorithm agility.
- `giturl`: the **source-fetch URL**. Required in `source`/`mixed` registries; it is
  the one field a `binary`-only registry omits to withhold source. `commit`, `tree`,
  `deps`, `build`, `provides` remain in **every** registry kind — they are
  metadata/hashes, not source — so both resolution (§7) and build-key computation
  (§8.3, needed to match a binary) work without source access.
- `deps`, `build`, `provides`, `ext`: copied from the package's `cosm.json` at the
  tagged commit. `deps` are **direct only**.

There is deliberately **no `buildlist.json`** in the registry (§7).

### 4.5 Binary specs (binary / mixed registries)
A spec in a `binary` or `mixed` registry additionally carries a `binaries` array — one
entry per prebuilt `{platform, toolchain, config}` (i.e. per build key). The bytes live
in an external artifact store, referenced by URL + hash:

```json
{
  "schemaVersion": 1,
  "name": "linalg", "uuid": "66c2…", "version": "v0.1.0",
  "commit": "…", "tree": "sha256:…", "build": "cmake", "provides": ["linalg@v0"],
  "deps": { "d38cb13b-…@v0": { "name": "terrastd", "version": "v0.6.4" } },
  "binaries": [
    {
      "platform": { "os": "linux", "arch": "amd64" },
      "toolchain": "gcc-13-libstdc++",
      "config": { "buildType": "Release" },
      "buildKey": "sha256:…",
      "deps": [ { "uuid": "d38cb13b-…", "version": "v0.6.4", "buildKey": "sha256:…" } ],
      "artifact": { "url": "https://artifacts.corp/linalg/v0.1.0/linux-amd64.tar.zst",
                    "hash": "sha256:…", "size": 1234567 },
      "descriptor": { "cmakePrefixPath": ["<prefix>"], "libDirs": ["<prefix>/lib"] }
    }
  ]
}
```

`buildKey` is the consumer-matchable cache key (§8.3); `descriptor` is the consumption
descriptor the extension's `build` would have emitted (§9), bundled so the consumer
skips building. See §8.5 for how a consumer selects and materializes a binary.

---

## 5. Depot layout

`$COSM_DEPOT` (default `~/.cosm`; resolved from config §11.3, not the shell profile).

```
$COSM_DEPOT/
  config.json                       # depot config (schemaVersion, defaults)
  registries/
    registries.json                 # [{ "name": "...", "uuid": "...", "giturl": "..." }]
    <registryName>/                 # a git clone of the registry
  mirrors/
    <uuid>.git/                     # bare mirror of a package repo (fetch cache)
  sources/
    <uuid>/<commit>/                # immutable exported source tree (verified)
  builds/
    <uuid>/<commit>/<buildKey>/
      artifacts/                    # extension-produced install prefix / files
      descriptor.json               # consumption descriptor for this build
      meta.json                     # { buildKey, platform, config, extVersion, deps }
  cache/
    buildlists/<uuid>/<commit>.json # memoized MVS closure (see §7.3)
  extensions/
    <extId>/                        # optional bundled extension install
  dev/
    workspace.json                  # available shared checkouts (§12.7)
    <name>@v<major>/                # shared clone under co-development (monorepo-like)
  logs/
    <timestamp>-<op>.log            # build/op logs (referenced by errors)
```

Notes:
- **Internal objects are keyed by UUID**, not name (the prototype mixed the two).
- `registries.json` upgrades from a `[]string` to structured entries so renames and
  duplicate URLs are detectable.
- `sources` are platform-independent; `builds` are platform/config-specific (§8).
- Each **project** (not the depot) has a git-ignored `.cosm/` holding local generated
  state — `develop.json` (enrollment, §7.4), generated env file(s), and local caches;
  never committed.
- Depot-mutating operations take an exclusive lock (`$COSM_DEPOT/.lock`) to serialize
  concurrent `cosm` processes.
- **Git authentication** is delegated to the user's git configuration; cosm shells out
  to `git` and inherits its auth, storing no credentials. **SSH is the recommended
  default** transport — one key-based mechanism serves public and private repos alike
  with no interactive prompts (important for non-interactive `build`/CI), so cosm's own
  scaffolding (`registry init`, `init`) defaults to SSH-form URLs. Since the giturl
  recorded in a registry is fixed, per-user transport flexibility comes from git's
  `url.<base>.insteadOf` rewrites (map `https://host/ ↔ git@host:`): an anonymous public
  consumer can use HTTPS while private users use SSH, without changing the registry.
  HTTPS (token / credential-helper) remains fully supported.

---

## 6. Versioning & identity

### 6.1 Version strings
Full SemVer 2.0.0 with a leading `v`: `v<major>.<minor>.<patch>[-<prerelease>][+<build>]`.
The implementation MUST use a well-tested semver library (e.g.
`golang.org/x/mod/semver` or `Masterminds/semver/v3`) — no hand-rolled parser.
Precedence, prerelease ordering (`v1.0.0-alpha < v1.0.0`), and build-metadata
(ignored for precedence) follow the spec. Partial versions (`v1`, `v1.2`) are
accepted only as *constraints* in `upgrade`/`add` (§12), never as identities.

### 6.2 A version = a git tag → a commit
A released version is a git tag matching §6.1. `release` creates and pushes that tag
in the package repo (it does not touch any registry). When the version is registered
(`registry add`/`update`), the tag is resolved to its commit (`git rev-list -n1
<tag>`; works for lightweight and annotated tags), and both the commit and the source
**content hash** are written into the registry's `specs.json`.

### 6.3 Compatibility unit (dependency key)
The dependency map key is `<uuid>@v<major>`. Two versions sharing a key are treated
as mutually compatible and are subject to MVS max-selection. Consequences:
- `foo@v0` and `foo@v1` are independent (can coexist in one project).
- **v0 policy (explicit):** all `v0.x` share key `@v0` and are treated as one
  compatibility line (matching Go and the prototype's on-disk `@v0` namespace).
  Because SemVer gives 0.x no compatibility guarantee, resolving a higher `v0.y`
  than a dependency declared MUST emit a **warning** (`W_V0_MINOR_BUMP`) naming the
  packages involved. `--strict` promotes it to an error. (Rationale: keying v0 by
  major.minor would fracture the module-namespace directory convention `std@v0`;
  the warning preserves that convention while flagging the real risk.)

### 6.4 Content hash (`tree`)
Deterministic hash of the exported source tree (post-export, §8.1): sha256 over a
canonical, sorted walk of file paths + modes + contents (git metadata excluded).
The exact serialization MUST be specified once and frozen (e.g. reuse `git archive`
+ streaming sha256, or an explicit `path\0mode\0len\0bytes` framing). Recorded at
registration; verified on every materialization (§8.1).

### 6.5 Moved-tag / tamper detection
When `registry update` re-observes a tag whose commit differs from the recorded
`commit`, or when a materialized tree's content hash differs from `tree`, the
operation fails with `E_INTEGRITY_MISMATCH` (never a silent update). Overriding
requires explicit `registry rm` + re-add.

---

## 7. Resolution (MVS)

### 7.1 Algorithm (normative)
Canonical MVS as in `cmd/go/internal/mvs`: a **visited-once traversal over direct
requirement edges**, not expansion of stored closures.

```
resolve(project) -> buildList:
    selected := {}                      # key <uuid>@vMAJOR -> chosen version
    work := queue(project.deps entries) # direct deps (floors)
    seen := set()                       # (uuid@major, version) already expanded
    while work not empty:
        (key, version) := work.pop()
        # MVS max: keep the highest floor seen for this compatibility unit
        if key in selected and semver_ge(selected[key], version): 
            # still must ensure the *higher* version's reqs are expanded
            pass
        else:
            selected[key] = max(selected[key], version)
        if (key, version) in seen: continue
        seen.add((key, version))
        specs := loadSpecs(key, version)   # develop overlay (§7.4) wins, else local registry
        for (dkey, dep) in specs.deps:     # direct deps only
            work.push((dkey, dep.version))
    # After fixpoint, selected[key] is the max floor; but a lower version's reqs may
    # reference a higher transitive floor — the queue above handles this because we
    # expand every (key,version) pair once. Final selection = selected[].
    return buildList(selected)
```

Complexity: **O(|B| + E)** local file reads and merges, where |B| is the reachable
version set and E the requirement edges. Each `(unit, version)` pair is expanded at
most once (`seen`), preventing the exponential blowup Cox warns about. Reads are
local (registry is a git clone), so there is no per-node network cost.

Note: the implementation MUST expand the requirements of every distinct version it
encounters for a compatibility unit, not only the final winner, because a lower
version can transitively raise a floor elsewhere. (This is standard MVS; the `seen`
set is over `(unit, version)` pairs, `selected` is over `unit`.)

`loadSpecs(key, version)` locates the package by the dependency's `name` (registry
shard/path) and verifies the `uuid`; if the unit is develop-overridden (§7.4) it
reads the dev checkout's live `cosm.json` instead. `buildList(selected)` enriches each
selected unit with its `commit`, `tree`, `giturl`, `build`, `provides`, and source
path (the registry commit, or the dev checkout when overridden). The closure cache
(§7.3) yields an identical result to this traversal.

### 7.2 Determinism
Given the same registry state (git commit) and the same `cosm.json`, resolution
is deterministic. No lockfile is needed to reproduce version selection.

### 7.3 Closure cache (efficiency, optional but recommended)
To recover the prototype's O(direct-deps) consume-time speed **without** putting
closures in the registry, memoize each version's transitive closure locally:

```
cache/buildlists/<uuid>/<commit>.json   # closure(version) = list of selected deps
```

- Keyed by immutable `commit` ⇒ never stale (specs change → new tag → new commit).
- On resolve, a dependent may merge its direct deps' cached closures (O(d)) instead
  of full traversal; a cache miss falls back to §7.1 and populates the cache.
- The cache is a pure optimization: deleting `cache/` MUST NOT change results.
- It is **local depot state**, never committed to any registry (this is the sole
  but decisive change from the prototype's design).

### 7.4 Develop overrides (shared checkouts, per-project opt-in)
`$COSM_DEPOT/dev/` holds **shared co-development checkouts** (§12.7), enumerated in
`dev/workspace.json` — one working tree per package, edited once and reusable across
projects (the monorepo benefit). A project uses a checkout only if it **opts in**:
each project records the units it enrolls in a git-ignored, project-local
`.cosm/develop.json`:

```json
{ "schemaVersion": 1, "enrolled": ["d38cb13b-…@v0", "4510efb2-…@v1"] }
```

Resolution rule: a compatibility unit `<uuid>@vMAJOR` is overridden **iff** it is
**enrolled by the current project**. Enrollment is authoritative; if an enrolled unit
has no shared checkout yet, one is **created on demand** — cloned into
`$COSM_DEPOT/dev/<name>@v<major>` at the recorded ref, defaulting to the repo's
**default branch** (`main`) — so the workspace self-heals (e.g. after another project
`--purge`d it). Where the override applies, the registry version is replaced by the
shared checkout: its live `cosm.json` and `deps` are used and it wins regardless of
version (like a `replace`). The override applies wherever that unit appears in the
graph (root or transitive), so enrolling a whole stack (`cosm develop A B C`) makes
those packages resolve to each other's dev trees. Build-list entries for overridden
units are marked `develop:true`; `status` and `build` report active overrides.

### 7.5 Downgrade (MVS downgrade algorithm)
`cosm downgrade <name> v<x>` is **not** a single-floor edit. Because MVS never
selects below any requirement, lowering `<name>` to `v<x>` requires also lowering
every requirement that would otherwise force it back up. The implementation MUST use
the MVS **downgrade** algorithm (Cox): for each module, if its requirement on
`<name>` exceeds `v<x>`, downgrade that module to its newest version whose
requirement on `<name>` is ≤ `v<x>` (recursively), removing modules with no such
version. If the target is unsatisfiable, fail with `E_RESOLUTION_CONFLICT` naming
the blocking requirer.

### 7.6 Test dependencies (non-transitive)

A manifest MAY declare `testDeps` (§4.1): dependencies needed to build and run the
package's own tests but not to use the package. They differ from `deps` in exactly
two ways:

1. **Seeded only for the root.** `cosm test` seeds resolution with the root's
   `deps` **and** `testDeps`; every other command (`build`, `run`, `status`, and
   resolution of the package *as a dependency*) seeds `deps` only. A test dep's own
   regular `deps` closure is pulled in normally; its own `testDeps` are never
   followed. Consequently a test dep can raise the selected version of a shared unit
   — but only in the test resolution, never in the shipping build.

2. **Never recorded in specs.** Registration/publication copies `deps` into
   `specs.json` but omits `testDeps`. A dependency's test deps therefore cannot be
   read by any consumer, making non-transitivity structural rather than a traversal
   rule. `deps` and `testDeps` MUST be disjoint by compatibility unit.

`cosm add <name> --test` writes to `testDeps`; `cosm rm <name>` removes from
whichever map holds it.

---

## 8. Materialization & build pipeline

Given a resolved build list, the core produces a ready-to-use environment.

### 8.1 Materialize sources
For each selected version (and the root project):
1. Ensure a bare mirror `mirrors/<uuid>.git` exists (clone/fetch as needed — the
   only network step, and only on cache miss or explicit `update`).
2. Export the commit to `sources/<uuid>/<commit>/` via `git archive <commit>`
   (never an in-place `checkout`; no shared mutable clone).
3. Compute the content hash of the exported tree and verify it equals `specs.tree`
   (`E_INTEGRITY_MISMATCH` on mismatch). Skip if `sources/<uuid>/<commit>/` already
   exists and is marked verified.

Nodes overridden by the develop workspace (§7.4) skip steps 1–3: their source **is**
the workspace checkout, used in place (integrity is not verified for a mutable
working tree). Nodes resolved from a **binary** registry skip §8.1–8.2 entirely (§8.5).

### 8.2 Topological build
Order the build list so dependencies build before dependents. For each node, in
order:
1. Compute its **build key** (§8.3).
2. If `builds/<uuid>/<commit>/<buildKey>/` exists, reuse it (cache hit).
3. Else invoke the owning extension's `build` verb (§9.3) with the source dir, a
   fresh install prefix under that build dir, and the **consumption descriptors of
   its full transitive dependency closure** (not just direct deps — a dependency's
   installed package config may `find_dependency()` a transitive one, so the whole
   closure's prefixes must be visible). The build **key** still keys off direct
   deps, whose own keys already encode the closure's content. On success, store the
   returned descriptor and metadata.
4. The extension writes only inside the provided prefix; the core owns cache paths.

A node resolved from a **binary** registry is not built here: at the same point in the
topological order it is materialized per §8.5 (download → verify hash → unpack →
adopt bundled descriptor), after which its descriptor feeds dependents exactly like a
locally-built node.

### 8.3 Build key
```
buildKey = sha256(join(
  "cosm-buildkey-v1",
  specs.tree,                       # source content
  platform.os, platform.arch,
  toolchainId,                      # extension-reported (compiler id+version, etc.)
  config.canonicalJSON,             # build type & options (Debug/Release, flags)
  extId, extVersion,
  sortedByUuid(dep.buildKey for dep in directDeps)   # DAG-aware invalidation
))
```
A change to any transitive dependency propagates up (a dep's buildKey feeds its
dependents'), giving correct incremental rebuilds. No-build languages (Lua) still
get a stable key; their `build` is effectively a copy/link + descriptor. For
develop-workspace nodes (§7.4) **and the root project** — both built from a mutable
working tree — `specs.tree` is replaced by a hash of the current working tree (or a
monotonic change token), so edits invalidate that node's and its dependents' build
keys and trigger rebuilds; for no-build languages edits are visible immediately via
the import path.

### 8.4 Environment assembly & delivery
The environment is a **pure function of the resolved build list**, not mutable state.
The core calls the root project extension's `activate` verb (§9.3) with the root
project and all dep descriptors; the extension returns `env` (verbatim variables) and
`prependPaths` (ordered path fragments). The core merges `prependPaths` in dependency
order onto the inherited environment. The core knows none of the variable names
(`LUA_PATH`, `CMAKE_PREFIX_PATH`, `DYLD_LIBRARY_PATH`, …) — they come from the
extension.

The assembled environment is exposed through several **delivery mechanisms**
(§12.3) — one-shot `run`, printable `env`, an opt-in interactive `shell`, and
optional direnv — rather than a single spawned subshell. It is recomputed only when
**stale** (`cosm.json` or the resolved build list changed), never per-command; there
is no prompt hijacking and no per-command re-sourcing (the prototype's bash
`DEBUG`-trap hack is removed).

### 8.5 Binary artifacts (source vs binary registries)
A registry is `source`, `binary`, or `mixed` (§4.2). **Resolution (§7) is identical for
all** — it operates on `uuid`/`version`/`deps`, which every spec carries — so a package
resolves the same whether the consumer sees its source or only its binaries. Only
materialization differs:
- **Source spec** → §8.1–8.2: fetch/export/verify source, then extension `build`.
- **Binary spec** → select the `binaries[]` entry whose recorded `buildKey` equals the
  consumer's computed build key (§8.3); verify its `artifact.hash`; unpack into
  `builds/<uuid>/<commit>/<buildKey>/artifacts`; store its bundled `descriptor`. The
  extension `build` is **skipped**. If no entry matches and no source is accessible,
  fail with `E_NO_BINARY` (listing the available `{platform,toolchain,config}` tags).

Access is enforced entirely by git/host permissions on the registries (and,
separately, on the artifact store): a low-clearance user is given only the binary
registry and never obtains source; a high-clearance user uses the source registry.
**cosm implements no ACLs of its own.** A `mixed` registry lets a single user prefer a
matching binary and fall back to building from source.

Exact-`buildKey` matching guarantees ABI/toolchain safety and suits a standardized
corporate toolchain (publisher and consumer share toolchain + transitive dep graph).
Looser, language-specific binary compatibility (e.g. an ABI tag instead of an exact
key) is an optional extension hook — §15.

Publishing binaries (`cosm publish`, §12.5) builds a package for target
platforms/toolchains, uploads each artifact to the store, and records the `binaries[]`
metadata (buildKey, hash, descriptor) into the binary registry via an atomic commit.

---

## 9. Extension interface (normative)

### 9.1 Model
Extensions are **executables** the core invokes; they communicate over a versioned
JSON protocol on stdin/stdout. This is required because build systems (CMake) need
real logic, and it keeps the core free of language code. In-process/built-in
extensions are NOT part of vNext.

### 9.2 Discovery & selection
- An extension for id `X` is an executable named `cosm-ext-X` found on `PATH` or in
  `$COSM_DEPOT/extensions/X/`. Depot installs take precedence.
- A package selects its extension via `cosm.json.build` (= `X`).
- The core calls `info` (§9.3) once per extension per run to negotiate protocol
  version and capabilities; a protocol-major mismatch is `E_EXT_PROTOCOL`.

### 9.3 Protocol
Invocation: `cosm-ext-X <verb>`; request JSON on **stdin**; response JSON on
**stdout**; human/log output on **stderr**; exit 0 ⇒ success, non-zero ⇒ failure
(with a JSON error body on stdout when possible). Protocol version: `1`.

Verbs:

| Verb | Purpose |
|------|---------|
| `info` | Report identity/capabilities. |
| `scaffold` | Create a new package's source layout for `cosm init --build <ext>`, and return its module namespaces (`provides`) + any default `ext`. The core writes `cosm.json`; the extension does not. |
| `build` | Configure/compile/install a version into a prefix; emit a descriptor. |
| `test` | (optional) Configure/run the package's tests against the full test-closure prefixes; report pass/fail + test count (0 = vacuous-pass guardrail) + a captured log. |
| `activate` | Produce the run/test environment for a root project + its deps. |

`info` response:
```json
{ "extension": "cmake", "version": "0.1.0", "protocol": 1,
  "languages": ["c++","c"], "toolchainId": "clang-17.0.0-darwin-arm64",
  "capabilities": ["build","activate","scaffold"] }
```

`build` request:
```json
{
  "package": { "name": "linalg", "uuid": "...", "version": "v0.1.0",
               "source": "/…/sources/<uuid>/<commit>",
               "provides": ["linalg@v0"], "ext": { "cmake": { … } } },
  "prefix": "/…/builds/<uuid>/<commit>/<buildKey>/artifacts",
  "buildKey": "sha256:…",
  "platform": { "os": "darwin", "arch": "arm64" },
  "config": { "buildType": "Release", "options": { } },
  "deps": [
    { "name": "terratest", "uuid": "...", "version": "v1.0.1",
      "descriptor": { /* opaque, from that dep's build */ } }
  ],
  "jobs": 8
}
```

`build` response:
```json
{ "status": "ok",
  "descriptor": { /* opaque; stored as builds/.../descriptor.json */ },
  "artifacts": ["lib/liblinalg.dylib", "lib/cmake/linalg/linalgConfig.cmake"],
  "log": "/…/logs/…-build-linalg.log" }
```

`activate` request: `{ "project": {…}, "platform": {…}, "deps": [ { descriptor } … ] }`
`activate` response:
```json
{ "env": { "COSM_PROJECT": "linalg" },
  "prependPaths": { "CMAKE_PREFIX_PATH": ["/…/prefixA","/…/prefixB"],
                    "DYLD_LIBRARY_PATH": ["/…/prefixA/lib"] } }
```
The core merges `prependPaths` in dependency order and applies `env` verbatim.

### 9.4 Reference extension — Lua (`cosm-ext-lua`)
- `build`: no compilation; link/copy `src/` into the prefix; descriptor =
  `{ "luaPath": ["<prefix>/src/?.lua","<prefix>/src/?/init.lua"], "luaCPath": [...] }`
  using the package's `provides` namespaces to shape `?` roots.
- `activate`: merge deps' `luaPath`/`luaCPath` into `LUA_PATH`/`LUA_CPATH`
  (`;;`-terminated), add the root project's own `src`.
- `scaffold`: create `src/<name>@v<major>/<name>.lua` + a test stub, and return
  `provides: ["<name>@v<major>"]` (the core writes `cosm.json`).
- `test`: run each `test/*.lua` with `LUA_PATH` assembled from the test closure;
  a non-zero file is a failure. Skips (reports unknown count) when no `lua` is on
  PATH so the pipeline stays testable.

### 9.5 Reference extension — C++/CMake (`cosm-ext-cmake`)
- `build`:
  1. Generate `cosm-deps.cmake` setting `CMAKE_PREFIX_PATH` from every dep
     descriptor's install prefix in the request — the **full transitive closure**
     the core forwards (§8.2), so `find_dependency()` of a transitive dep resolves.
  2. `cmake -S <source> -B <buildtmp> -DCMAKE_INSTALL_PREFIX=<prefix>`
     `-DCMAKE_PREFIX_PATH=<deps> -DCMAKE_BUILD_TYPE=<config.buildType>`
     `-C cosm-deps.cmake` (+ options from `ext.cmake`).
  3. `cmake --build <buildtmp> -j <jobs>` then `cmake --install <buildtmp>`.
  4. descriptor = `{ "cmakePrefixPath": ["<prefix>"], "libDirs": ["<prefix>/lib"],
     "binDirs": ["<prefix>/bin"], "includeDirs": ["<prefix>/include"] }`.
  The package's own `CMakeLists.txt` uses ordinary `find_package(<dep> CONFIG)` and
  `install(EXPORT …)`; vendored C under `external/` is package source, not a cosm dep.
- `activate`: set `CMAKE_PREFIX_PATH` (all dep prefixes), `DYLD_LIBRARY_PATH`
  (darwin) / `LD_LIBRARY_PATH` (linux) from `libDirs`, `PATH` from `binDirs`.
  `toolchainId` reflects compiler id+version so the build key changes across
  toolchains.
- `test`: configure the project **source** fresh with `CMAKE_PREFIX_PATH` spanning
  the full test closure (regular deps + testDeps, so `find_package(<testdep>)` such
  as Catch2 resolves), `cmake --build`, then `ctest` (forwarding `--verbose`/`-- args`)
  in a captured log. Reports failed on a ctest failure and the test count parsed from
  ctest output (0 → the core's vacuous-pass guardrail fires).
- `scaffold`: create a starter `CMakeLists.txt`, `include/<name>_v<major>/<name>.hpp`,
  and `src/<name>.cpp` (the versioned C++ binding of the namespace, §4.1), and return
  `provides: ["<name>@v<major>"]` plus a default `ext.cmake.minimumVersion` (the core
  writes `cosm.json`).
- Platform: derive `.dylib`/`.so`(/`.dll` later) from `platform.os`.

### 9.6 Extension guarantees
- Deterministic given identical inputs (so build keys are meaningful).
- Writes only inside the provided `prefix`; treats `source` as read-only.
- Emits actionable failures on stderr and a non-zero exit; the tail is surfaced by
  the core (§10.3).

---

## 10. Error handling

### 10.1 Typed taxonomy
Core defines exported error values/types matched via `errors.Is/As` (no message
sniffing). At least:

```
E_USAGE               invalid args/flags
E_NO_PROJECT          no cosm.json where required
E_REGISTRY_NOT_FOUND  unknown registry
E_PACKAGE_NOT_FOUND   package/version not in any registry
E_VERSION_EXISTS      release/registry-add of an existing version
E_DIRTY_WORKTREE      uncommitted changes on release
E_NOT_IN_SYNC         local behind origin on release
E_TAG_MOVED           tag→commit changed vs recorded
E_INTEGRITY_MISMATCH  content hash mismatch
E_RESOLUTION_CONFLICT MVS could not satisfy constraints
E_EXT_NOT_FOUND       no extension for `build` id
E_EXT_PROTOCOL        extension protocol/version mismatch
E_BUILD_FAILED        {package, phase, logPath}
E_NO_BINARY           no matching binary artifact and no source access (§8.5)
E_NETWORK             fetch/push failure
E_INTERNAL            bug / invariant violation
```
Plus warnings, e.g. `W_V0_MINOR_BUMP` (§6.3).

### 10.2 Exit codes
`0` ok; `1` generic failure; `2` usage (`E_USAGE`); `3` not-found
(`E_*_NOT_FOUND`); `4` network (`E_NETWORK`); `5` integrity/tag
(`E_INTEGRITY_MISMATCH`,`E_TAG_MOVED`); `6` build (`E_BUILD_FAILED`); `70` internal.
Messages go to stderr as `error: <what>\n  hint: <fix>`.

### 10.3 Rules
- **Atomicity**: every mutating op stages into a temp dir/index and commits-or-aborts;
  no partial registry/version writes. Registry pushes use fetch+rebase-retry.
- **Extension noise containment**: raw cmake/make output → `logs/…`; only the tail is
  echoed unless `--verbose`. `E_BUILD_FAILED` points at the log.
- **No side-effect I/O in library code**: functions return data/errors; the CLI layer
  renders. (Removes the prototype's buried `fmt.Print`/`Warning:` calls.)
- **Warn vs fail**: default fail-fast; `--strict` promotes warnings to errors;
  "warn and continue" only for genuinely recoverable, clearly-logged cases.

---

## 11. Depot & configuration

### 11.1 `cosm setup`
Explicit, idempotent. Creates `$COSM_DEPOT` structure (§5), writes `config.json`,
and prints the `export COSM_DEPOT=…` line for the user to add to their profile.
**Does not silently edit shell profiles** (the prototype's `.zprofile` mutation is
removed).

### 11.2 Depot bootstrap
On any command, if `$COSM_DEPOT` is unset it is resolved from (in order):
`--depot` flag, `COSM_DEPOT` env, `config.json` at `${XDG_CONFIG_HOME:-~/.config}/cosm/`,
else default `~/.cosm`. A missing/incomplete depot triggers a clear
`hint: run 'cosm setup'` rather than interactive prompting mid-command.

### 11.3 `config.json`
```json
{ "schemaVersion": 1, "depot": "/Users/ekke/.cosm",
  "defaultShell": "bash", "templatesRepo": "https://github.com/simkinetic/cosm-templates.git" }
```

---

## 12. Command-line interface

git-like; cobra or equivalent. Global flags on every command:
`-v/--verbose`, `-q/--quiet`, `--json` (machine-readable stdout), `--offline`,
`--frozen` (fail if resolution would need a network fetch), `-C/--dir <path>`,
`--depot <path>`.

### 12.1 Project lifecycle
- `cosm init <name> [v<version>] [--build <ext>]`
  Creates `cosm.json` (default `v0.1.0`). With `--build`, delegates skeleton creation
  to that extension's `scaffold` (substitutes name **and** major in paths and
  contents, fixing the prototype's unrenamed `PkgTemplate@vMAJOR` bug); the returned
  `provides`/`ext` are folded into the manifest the core writes. If the extension is
  absent, the manifest is still written (with a note). Plain `init` (no `--build`)
  writes only `cosm.json`.
- `cosm status` — resolves (offline) and prints the project, its direct deps (bold),
  and the resolved build list with selected versions; flags v0 minor bumps and
  develop-mode packages.

### 12.2 Dependencies
- `cosm add <name> [v<version>] [--registry <r>] [--major <n>] [--test] [--offline]` —
  look up in local registries (prompt on multi-registry match), add to `deps` (or
  `testDeps` with `--test`) keyed by `<uuid>@vMAJOR`. **Offline when the local clone
  already has the package/version** (no network, deterministic); only on a miss does
  it perform a **lazy, targeted pull** of the relevant registry (the pinned one, else
  the one already holding the package, else all) once and retry, so a version
  published since the last `cosm update` still resolves. `--offline` (or `COSM_OFFLINE`)
  suppresses the pull for reproducible/CI resolution. `add` never mutates a registry.
  If `<name>` is not in any registry but has been adopted into the dev workspace
  (§12.7, `cosm develop --path`), fall back to that local checkout for identity — this
  lets a project depend on an **unpublished** sibling. A still-missing explicit
  version errors with the versions actually available.
- `cosm rm <name>` — remove (prompt if multiple majors present).
- `cosm upgrade <name> [v<x>|v<x.y>|v<x.y.z>] [--latest] [--all]` — raise the floor
  in `deps` conservatively (latest compatible) or to `--latest`. Partial constraints
  select the latest matching patch/minor. **`--latest` stays within the same major**
  (compatibility unit); moving to a new major is always an explicit
  `cosm add <name> v<N>` (coexist) → migrate imports → `cosm rm` the old major.
  Upgrading a *transitive* dependency injects/raises an explicit requirement for it
  in `cosm.json` (Go-style floor lift).
- `cosm downgrade <name> v<version>` — lower `<name>`'s floor to an existing version;
  runs the MVS downgrade algorithm (§7.5), not a single-floor edit.

### 12.3 Build, run & environment
The environment (§8.4) is computed once and offered through several delivery modes;
a spawned subshell is one option, not the only one.

- `cosm build [--release|--debug] [--jobs N] [--offline]` — resolve → materialize →
  topological build via extensions. Reuses the artifact cache.
- `cosm test [--verbose] [-- <runner args>]` — build the test closure (regular deps
  **and** `testDeps`, §7.6), then invoke the extension's `test` verb (§9.3) with each
  dep's install **prefix**, so tests gated on a test-only dependency configure and
  run. The extension reports pass/fail and a test count; `cosm test` fails on a
  failing test and on a **zero-test run** (vacuous-pass guardrail), surfacing the
  captured output on failure (always with `--verbose`). Args after `--` forward to the
  runner (e.g. `ctest`).
- `cosm run [--] <cmd> [args…]` — **primary execution primitive.** Build if needed,
  then exec `<cmd>` once with the assembled environment. Ephemeral, reproducible,
  works in any shell / CI / editor. E.g. `cosm run -- lua src/main.lua`,
  `cosm run -- ./build/app`.
- `cosm env [--shell bash|zsh|fish|powershell] [--json] [--direnv]` — print the
  assembled environment in the requested shell syntax (default: detected `$SHELL`),
  as JSON, or as a direnv `.envrc` (`use cosm`). Lets the user load it into their
  **own** shell (`eval "$(cosm env)"`) or feed other tools. Cross-shell; no subshell.
- `cosm shell` (alias `cosm activate`) — **opt-in** interactive convenience: launch
  the user's shell (detected; bash/zsh/fish-aware) with the environment applied via a
  generated shell-appropriate init and a `cosm>` prompt marker. Implemented as a thin
  wrapper over `cosm env`; not a bash-only rcfile hack, and not the only entry point.

### 12.4 Release
- `cosm release [v<version>|--patch|--minor|--major] [--registry <r>]` — require
  clean worktree + in-sync origin; bump `cosm.json`; commit; tag; push branch+tag.
  Rejects an existing/lesser version.

### 12.5 Registry
- `cosm registry init <name> <giturl>` (empty remote), `clone <giturl>`,
  `add <name> <giturl> | <name> <pkg> v<ver>`, `rm <name> <pkg> [v<ver>] [--force]`,
  `delete <name> [--force]`.
- `cosm registry status <name>` — overview of one registry (evaluable anywhere):
  each registered package with its UUID and git URL, its known versions, and the
  registry clone's git sync state vs its remote (up-to-date / ahead / behind) and
  last update. `--json` for machine output. (Distinct from project `cosm status`,
  §12.1.)
- `registry add` clones/mirrors the package, records `specs.json` (commit + content
  hash) for each new tag, and commits atomically. **No push-on-read anywhere.**
- `cosm publish [--registry <r>] [--platform …] [--toolchain …] [--config …]` — build
  the current package for the target(s), upload each artifact to the store, and record
  its `binaries[]` metadata (buildKey + hash + descriptor) into a `binary`/`mixed`
  registry (atomic commit). Registers to a binary registry what `release`+`registry add`
  do for a source registry (§8.5).

### 12.6 Sync
- `cosm update [<name> | --all]` — the explicit, whole-registry network step: `git
  pull` each registry and record any new upstream tags (with integrity checks, §6.5).
  `cosm status` never touches the network. `cosm add` is offline on a hit, but on a
  **miss** it performs a lazy, targeted pull of the relevant registry and retries
  (§12.2) — a self-heal for stale clones — unless `--offline`/`COSM_OFFLINE` is set.
  Neither command discovers tags for a package the local registries don't reference.

### 12.7 Develop (shared checkouts, per-project opt-in)
`develop` maintains shared co-development checkouts under `$COSM_DEPOT/dev/` (edit a
package once, reuse across projects) and, separately, **enrolls the current project**
to use them (per-project opt-in, §7.4). Two pieces of state: the depot
`dev/workspace.json` (available shared checkouts) and each project's git-ignored
`.cosm/develop.json` (units this project opted into). `workspace.json`:

```json
{ "schemaVersion": 1,
  "entries": [
    { "name": "terrastd", "uuid": "d38cb13b-…", "major": 0,
      "giturl": "git@github.com:simkinetic/terrastd.git",
      "ref": "main", "refKind": "branch", "path": "dev/terrastd@v0" }
  ] }
```

- `cosm develop <name> [--major <N>] [--branch <b> | --tag v<x.y.z>] [--path <dir>]` —
  **enroll the current project** (add the unit to `.cosm/develop.json`) and ensure a
  shared checkout exists at `$COSM_DEPOT/dev/<name>@v<major>`. Source precedence: an
  already-adopted workspace unit; then, with `--path`, a local checkout; else a
  resolved dependency cloned from its registry URL. The ref defaults to the repo's
  **default branch** (so you can commit); use `--branch`/`--tag` for another ref
  (recorded in `dev/workspace.json`). Disambiguate a multi-major dependency with
  `--major`. Only enrolled projects resolve to the dev tree (§7.4); enrollment is
  authoritative, so an enrolled unit is re-cloned on demand if its checkout is removed.
- `--path <dir>` **bootstraps a new, unpublished sibling**: it adopts the local
  package at `<dir>` (identity from its `cosm.json`) by symlinking it into the
  workspace (entry marked `"local": true`), so it needn't exist in any registry.
  Combine with `cosm add <name>` — whose registry-miss fallback reads the adopted
  checkout — to declare the dependency, resolving the chicken-and-egg where `add`
  needs a registry and `develop` needs a resolved dependency. The project is not
  releasable until the sibling is published.
- `cosm develop --list` — show shared checkouts and, for the current project, which
  are enrolled, each with its ref and uncommitted-changes state.
- `cosm free <name> [--major <N>] [--purge]` — un-enroll the current project (remove from
  `.cosm/develop.json`); this project's resolution reverts to the released version.
  The shared checkout is retained (other projects may still use it); `--purge` deletes
  the shared checkout once no project references it (refusing on uncommitted changes
  unless `--force`). For a `--path`-adopted (`"local"`) unit, `--purge` removes only
  the workspace symlink, never the working directory.
- Typical flow: `cosm develop A B C` (checkout + enroll the stack) → edit → in a dev
  checkout `cosm release --patch` + `cosm registry add` → `cosm free` →
  `cosm upgrade`.

---

## 13. Reproducibility & integrity model

- **Version selection** is reproducible from `cosm.json` + registry git state via
  MVS (§7). No lockfile.
- **Content integrity** is anchored by `specs.tree` recorded in the registry's git
  history (itself content-addressed by git) and verified on every materialization
  (§8.1). Moved tags / tampering surface as `E_INTEGRITY_MISMATCH`/`E_TAG_MOVED`.
- Therefore the "no lockfile" property (version selection) and integrity are
  orthogonal and both hold. No per-project `cosm.sum` is required; the registry is
  the hash authority. (A future opt-in project hash file MAY be added for
  air-gapped verification; out of scope for vNext.)

---

## 14. Migration from the prototype (one-off)

A `cosm migrate` helper (or a documented manual path) SHOULD:
1. Rename `Project.json` → `cosm.json`; add `schemaVersion`, `build` (from `language`),
   `provides` (from the `src/<ns>@v<major>` directory, confirmed with the user),
   structured `authors`.
2. Drop `buildlist.json` from registries; keep `specs.json`/`versions.json`
   (add `commit`+`tree` by re-resolving tags).
3. Rebuild depot caches from scratch (`sources/`, `builds/`, `cache/` are
   regenerable).

---

## 15. Deferred / future work
- Cross-language dependency edges and a shared descriptor vocabulary.
- Go-1.17-style graph pruning (record a pruned transitive set in the consumer to
  avoid loading irrelevant specs) — only if depot scale ever demands it; the
  closure cache (§7.3) covers current needs.
- Windows (`.dll`, `%PATH%`, `.env.ps1`).
- Registry signing / provenance beyond content hashes.
- Parallel builds across independent DAG branches.
- Looser, language-specific binary compatibility (ABI tags) so one binary matches a
  range of consumer toolchains, not only an exact build key (§8.5).

---

## 16. Testing & quality

### 16.1 Testability by design
The prototype's core was hard to test because git, network, and filesystem I/O were
inlined into command logic (tests had to spawn real git and the real binary). vNext
MUST isolate every side-effecting boundary behind an interface so decision logic is
pure and unit-testable with fakes:
- `Git` (clone, fetch, archive, rev-list, tag, ls-remote, push),
- `Depot`/filesystem (or `io/fs` + an in-memory implementation),
- `ExtensionRunner` (invoke an extension verb with a JSON request),
- `ArtifactStore` (fetch/put binary artifacts),
- `Prompter` (stdin), `Clock`, and an injectable UUID source.

The resolver, MVS downgrade, build-key computation, semver, manifest (de)serialization,
descriptor merge, and env assembly are then **pure functions over data** and need no
I/O to test.

### 16.2 Test layers
1. **Unit (fast, no I/O)** — the bulk of the suite: semver parse/precedence (incl.
   prerelease/build, partial constraints); MVS resolve over in-memory spec graphs (the
   A–G graph with its known build list, diamonds, the non-selected-lower-version floor
   case §7.1, the v0 warning, `E_RESOLUTION_CONFLICT`); the downgrade algorithm (§7.5,
   incl. cascading and unsatisfiable targets); build-key determinism and DAG-aware
   invalidation; manifest schema round-trips (golden files); shard/path logic;
   error→exit-code mapping; binary selection/matching (§8.5).
2. **Property-based** (rapid/gopter) for resolver invariants: selected ≥ every
   requirement; determinism under input reordering; a redundant lower requirement never
   changes the result; closure-cache result (§7.3) ≡ full traversal (§7.1).
3. **Contract tests** for the two external boundaries: `Git` against a **real local
   git** using temp `file://` bare repos (behind the interface, so only these tests need
   git); the extension protocol against a **fake extension** (canned JSON) for wiring,
   and against the **real Lua and CMake extensions** on fixture packages.
4. **Integration / e2e** — drive the real `cosm` binary in a hermetic temp depot with
   local bare-repo registries and sample packages, covering full flows: setup → registry
   init/clone → init → release → registry add → add → resolve/build list → build (Lua
   no-op + a small C++/CMake sample) → run/test → develop/free (shared checkout +
   per-project enroll) → upgrade/downgrade → **binary-registry consume** (publish a
   binary, then resolve+materialize it with no source access) → integrity-failure
   injection (move a tag / corrupt a tree → `E_INTEGRITY_MISMATCH`) → atomicity (fail
   mid-registry-write → assert no partial state) → depot-lock contention.

### 16.3 Fixtures & hermeticity
- A committed corpus of sample packages (Lua + C++) and a known dependency graph with
  **expected build lists** (reuse the A–G example). Deterministic and offline.
- Every test runs against a temp `$COSM_DEPOT`, temp `HOME`, temp `GIT_CONFIG_GLOBAL`,
  and `file://` remotes — **no network**. Injected clock and UUID source give stable
  golden output.

### 16.4 Coverage & CI
- **>90% line coverage, enforced as a CI gate** (`go test -coverprofile`, fail under
  threshold). The pure core should approach ~100%; thin I/O shells are covered by the
  contract/integration layers.
- Coverage % is necessary but not sufficient: a **must-cover behavior checklist** (every
  typed error path, the resolver cases above, integrity/atomicity/lock paths, source
  vs binary materialization) is tracked explicitly, so ">90%" is *confirmed* behavior
  coverage rather than incidental line coverage.
- CI matrix: linux + macOS (as today); binary/build-key tests tag artifacts by
  `toolchainId` so runner toolchain differences don't cause false matches.

---

## Appendix A — Key differences from the prototype
1. Registry no longer stores `buildlist.json`; MVS is computed on demand (canonical,
   O(|B|+E) local) with a local immutable closure cache for O(direct-deps) reuse.
2. Language/build logic moves entirely into out-of-process extensions; core is blind.
3. Sources (by commit) and builds (by build key) are separated; no in-place
   checkout/revert.
4. Content-hash integrity + moved-tag detection added.
5. Typed errors, stable exit codes, atomic registry writes, no push-on-read.
6. Structured manifests with `schemaVersion`, declared `provides`, structured
   `authors`; extension config isolated under `ext`.
7. Explicit `setup`/`update`/`build`/`run`; no shell-profile mutation; offline-first.
8. SSH-default transport with git `insteadOf` remapping; public + private alike.
9. Clearance-tiered **source vs binary registries** (§8.5) — identical resolution, ACLs
   via git/host permissions; binaries are a build-key-keyed remote artifact cache.
10. Testability-by-design (injected I/O boundaries) with a >90%-coverage gate and a
    must-cover behavior checklist (§16).
