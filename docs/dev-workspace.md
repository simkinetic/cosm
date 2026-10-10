# cosm co-development workspace — instructions for Claude Code

This directory (`$COSM_DEPOT/dev`) is a **cosm** co-development workspace. Each
subfolder `<name>@v<major>/` is a git checkout of a package under active
development. Your edits here are **live** for every project that has run
`cosm develop <name>` — changes take effect immediately, without any release.

cosm is an MVS-based, git-backed, language-agnostic package manager. Full
command/format reference:
<https://github.com/simkinetic/cosm/blob/main/docs/reference.md>

---

## Rule: always ask before publishing or touching a shared registry

Some cosm commands publish a release or mutate a shared registry. You MAY run
them — but only after **explicitly asking the user and getting a clear "yes",
every time, even in auto-accept / "yolo" mode.** A general "go ahead" on a task
does NOT cover these. When you ask, state plainly that it is a cosm command that
publishes / changes a shared resource, and show the exact command you intend to run.

Always confirm before running (each time, never silently):

- `cosm release …` — bumps the version, tags, and pushes a release.
- `cosm publish …` — uploads a binary artifact to a registry.
- `cosm registry add/rm/sync/init/clone/delete …` — mutates a shared registry.
- `git push …`, `git tag …` — pushing branches or tags to a shared remote.

Example ask: "Heads up — `cosm release --patch` will tag **v0.2.1** and push the
release. This is a cosm command that publishes, so it needs your approval. Run it?"

---

## What you may do freely

- **Read and edit source.** Modules live in `src/<namespace>@v<major>/`; the
  package manifest is `cosm.json`. The namespace is major-versioned (`foo@v0`) so
  two majors can coexist; where a language can't spell `@` (C++/CMake) the binding
  is `foo_v0` (include prefix, C++ namespace, CMake package/target).
- **Build** the current package and its dependencies: `cosm build`. It prints a line
  per package as it goes (`[2/5] linalg v0.3.0 (14.2s)`, or `(cached)`). For a long
  build, add `-v` to stream the compiler output live; `-v` works the same on `run`,
  `test`, `env`, and `shell`.
- **Run** a command in the built environment: `cosm run -- <cmd>`
  (e.g. `cosm run -- lua src/main.lua`, or `cosm run -- ./build/app`)
- **Test**: `cosm test` — builds with test-only deps and runs the suite. It is a
  trustworthy gate: it **fails** on a failing test AND on a zero-test run (so a
  mis-configured suite can't pass vacuously). `-v` streams the configure, build, and
  test output live; without it the output is shown only on failure. `-- <args>`
  forwards to the runner (e.g. `ctest`); `--keep-build` + `--cxxflags`/`--ldflags` let a
  coverage tool ride the run.
  - The tests see `COSM_TEST_JOBS` (the `--jobs` value, by default the CPU count) and
    `COSM_TEST_CPUS` (CPUs available). If you write or change a test harness that
    starts threads, size the pool from these, never from a hard-coded number or the
    raw core count: on a large machine, a runner that executes tests in parallel and
    gives every test all the cores oversubscribes badly.
- **Inspect** resolution: `cosm status` (shows the resolved build list; `(develop)`
  marks packages taken live from this workspace). `build`/`test`/`run`/`env`/`status`
  all report the same develop advisories, so they never disagree on what you build
  against.
- **Scaffold a new package**: `cosm init <name> --build <ext>` writes `cosm.json`
  (with `provides` already set) and the language-specific source layout for you —
  no hand-editing of `provides` or `src/<ns>/` needed.
- **Manage the current package's dependencies** — these only edit its local
  `cosm.json`, so they are safe:
  `cosm add <name> [vX.Y.Z]`, `cosm rm <name>`,
  `cosm upgrade <name> [--latest]`, `cosm downgrade <name> vX.Y.Z`
  - If a name is ambiguous (found in several registries, or spanning major lines)
    `cosm add` normally prompts. In non-interactive / auto mode, resolve it without
    a prompt using `cosm add <name> --registry <reg>` and/or `--major <n>`: these
    filter the candidates, so a single match is added silently and no match errors
    instead of blocking.
  - For a **test-only** dependency, use `cosm add <name> --test` (recorded in
    `testDeps`). It's available to `cosm test` but is not inherited by packages that
    depend on this one.
  - `cosm add` is offline when the version is already in the local registry clone; on
    a miss it does a read-only pull of the registry and retries (a self-heal for a
    stale clone — it never mutates a registry). Pass `--offline` (or set
    `COSM_OFFLINE`) for strictly no-network, reproducible resolution.
- **Get the environment** for other tools: `eval "$(cosm env)"`
- **Local git**: `git add` / `git commit` on a working branch are fine;
  `git push` / `git tag` require explicit confirmation (see the rule above).

## Typical loop

1. Edit code in the relevant `<name>@vN/` checkout.
2. `cosm build` (from that package's directory) to compile it and its deps.
3. `cosm run -- <cmd>` or `cosm test` to verify. Changes are live across every
   enrolled project — no release needed to try them.
4. When it's ready to ship, **ask** the user to approve the exact `cosm release …`
   command (per the rule above), and run it only on a clear "yes".

## Notes

- Run cosm commands from **inside** the specific package directory you're editing.
- `cosm develop <name>` / `cosm free <name>` manage which packages live in this
  workspace; `cosm develop --all` enrolls every workspace package this project depends
  on at once. Avoid `cosm free --purge` unless the user asks — it deletes a checkout
  and any uncommitted work (for a `--path`-adopted local package it removes only the
  symlink, not your working directory).
- **Co-developing a brand-new, unpublished package** (e.g. a sibling you're creating
  alongside the current one): registries aren't involved yet, so bootstrap it with
  `--path`. From the consuming project:
  1. `cosm init <name> --build <ext>` in some directory (creates the sibling).
  2. `cosm develop <name> --path <dir>` — adopts the local checkout into the workspace.
  3. `cosm add <name>` — the registry-miss fallback reads the adopted checkout and
     writes the dependency edge. Now edit both live; no release needed.
  The project isn't releasable until the sibling is published (`cosm release` +
  `cosm registry add`), which is a shared-resource step — **ask first** (see the rule
  above).
- **Checking something in a clean, throwaway depot** (e.g. that a released version
  builds from the registries alone): point cosm at it for those commands only, and
  don't pass `--set-default`:

  ```sh
  export COSM_DEPOT=$(mktemp -d)/depot && cosm setup   # your default depot is untouched
  ```

  `cosm setup` changes the global default only when no depot was named. If a warning
  says a develop checkout is missing, read the depot path it prints — the usual cause
  is running against a different depot than you meant. `cosm --version` shows the
  release, commit, and path of the binary that ran. If that isn't the cosm you
  expect, an older one is earlier on `PATH`; running the expected one by its full
  path with `--version` names the binary that plain `cosm` resolves to.
- **Detecting dependency changes** (cache keys, "did anything below me move?"): use
  `cosm status --hash` — a stable digest of the resolved dependencies that also moves
  when a co-developed checkout is edited in place. Add `--tests` to cover test-only
  dependencies. Don't hash `cosm.json` alone; it misses changes further down.
- **Working in a git worktree (parallel agents).** This workspace holds exactly one
  checkout per package, at `<name>@vN/`. If you work in a separate git worktree of a
  package, a plain `cosm build`/`cosm test` from another project still resolves that
  dependency to the workspace checkout — i.e. **someone else's tree**, not yours. Pass
  `--source` to redirect it for that command only:

  ```sh
  cosm test --source /path/to/worktree/<name>
  ```

  It is repeatable (once per redirected package), writes nothing to the workspace or
  enrollment, and prints the redirect. You do **not** need it for the package you are
  standing in — the current directory always wins for its own package. Test trees are
  keyed per checkout, so parallel `cosm test` runs no longer clobber each other.
- Version selection is **MVS**: each package declares minimum dependency versions
  and the maximum of the minimums is chosen. There is no lockfile; resolution is
  reproducible from the manifests.
- Moving to a new major is incremental: `cosm add <name> v<N>` (coexists with the
  old major) → migrate imports → `cosm rm` the old major.
