# Keeping a registry up to date (server-side)

cosm registries are maintained **server-side**, the way crates.io or the Go module
proxy work: the registry is the single writer, and clients only ever read
(`cosm update` = `git pull`). New package versions are discovered by the registry
itself, not by clients.

Two ways a version enters a registry:

1. **Explicit** — a maintainer runs `cosm registry add <registry> <package-giturl>`
   for immediate registration (it's idempotent and picks up any new tags).
2. **Automatic** — a scheduled job runs `cosm registry sync <registry>`, which
   scans every registered package's git remote for new semver tags and registers
   any that are missing, in one atomic commit + push.

Because only this job pushes to the registry, clients never need write access,
there are no concurrent-push conflicts, and read-only (binary-only) consumers stay
fully functional — they just pull.

## `cosm registry sync`

```sh
cosm registry sync myreg
# Registered somepkg@v1.4.0
# Registered otherpkg@v0.3.1
```

For each package it: ensures a local mirror of the package repo, fetches tags,
and registers every valid `vX.Y.Z` tag not already present (writing `specs.json`
with the resolved commit + content-hash `tree`, and appending `versions.json`).
Malformed tags (e.g. a tag whose `cosm.json` version doesn't match) are skipped
with a warning, not treated as fatal.

It honours each package's **registration policy** (`policy.json`): tags below the
package's floor (`registry add --from`) and versions removed with `registry rm` are
never registered, so a scheduled sync doesn't undo those choices. **Run sync with
cosm v0.1.7 or later**: an older cosm doesn't read the policy and would register
those versions again. To take a bad release out of use without breaking projects
that already require it, `registry yank` it rather than removing it.

## `cosm registry check`

```sh
cosm registry check myreg
# Checked every version: 57 versions of 21 packages in myreg (local clones).
#   FAIL  kinetic-rte v0.1.0 (myreg): requires cmaps-macro v0.1.0, which is in no registry (registered: v0.2.0)
# error: 1 of 57 registered versions don't resolve within the registries
```

Confirms that every registered version resolves inside the registries — each
declared dependency minimum is registered, and the version's whole closure resolves
from registries alone — and exits non-zero otherwise. That catches the state a
consumer would hit: a version still requiring a dependency version that was never
registered, or has since been removed. `--latest` checks only the newest version of
each major line. It is read-only.

It checks against the registries in the depot, so **clone every registry the
packages depend on**, not just this one; otherwise a dependency registered elsewhere
reads as missing.

## Sample GitHub Actions workflow

Drop this into the **registry repository** as `.github/workflows/sync.yml`. It
runs hourly (and on demand), clones the registry into a throwaway depot, syncs,
and pushes any new versions back.

```yaml
name: sync
on:
  schedule:
    - cron: "0 * * * *"   # hourly
  workflow_dispatch: {}

permissions:
  contents: write         # push new registrations back to this repo

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - name: Install cosm
        run: |
          curl -fsSL -o /usr/local/bin/cosm \
            https://github.com/simkinetic/cosm/releases/latest/download/cosm-linux-amd64
          chmod +x /usr/local/bin/cosm

      - name: Configure git
        run: |
          git config --global user.name "registry-bot"
          git config --global user.email "registry-bot@users.noreply.github.com"

      - name: Sync
        env:
          # A URL cosm can both read the registry from and push to. For GitHub,
          # embed the token; for private package remotes, add the relevant keys.
          REGISTRY_URL: "https://x-access-token:${{ secrets.GITHUB_TOKEN }}@github.com/${{ github.repository }}.git"
        run: |
          export COSM_DEPOT="$RUNNER_TEMP/.cosm"
          cosm setup
          cosm registry clone "$REGISTRY_URL"
          # The registry name is read from its registry.json:
          name="$(basename "$GITHUB_REPOSITORY")"
          cosm registry sync "$name"

      - name: Check every version resolves
        run: |
          export COSM_DEPOT="$RUNNER_TEMP/.cosm"
          # Clone any other registry the packages depend on first, e.g.:
          #   cosm registry clone https://github.com/<org>/<other-registry>.git
          cosm registry check
```

Notes:

- The job needs **read access to every package remote** it scans (public repos
  need nothing; private ones need a deploy key or PAT). It needs **write access to
  the registry repo** to push registrations (`GITHUB_TOKEN` with `contents:write`
  above).
- `sync` only requires `cosm` + `git` — no language extensions, because it reads
  each tag's `cosm.json` and records metadata; it does not build anything.
- Prefer a small, frequent cron for responsiveness; `sync` is cheap and a no-op
  when there is nothing new.
