# Tutorial: a Lua project with cosm

This walks through publishing a Lua **library** and consuming it from an **app** —
registry setup, releasing, resolving, building, running, and co-development.

Everything here is exercised by an integration test
(`internal/cli/tutorial_lua_test.go`), so the commands are known to work.

## Prerequisites

- `cosm`, `cosm-ext-lua` on your `PATH` (or install the extension under
  `$COSM_DEPOT/extensions/lua/cosm-ext-lua`). See the [README](../README.md).
- `lua` on your `PATH` to run the final program.
- `git` configured with a `user.name` / `user.email`.

## Understanding remotes (read this first)

cosm stores everything in **git repositories** ("remotes"):

- the **registry** is one git repo that indexes packages;
- **each package** (a library or app you publish) is its own git repo.

`cosm registry init` requires the registry's remote to be **empty** — it writes the
index and pushes the first commit itself.

**This tutorial needs no accounts or servers.** We simulate every remote with a
local *bare* repository and a `file://` URL, so you can copy-paste and run it all on
one machine. A "bare" repo is just a git repo with no working files — the form a
server stores. We create each one explicitly with `git init --bare -b main` right
before it is used. The `-b main` makes the bare repo's default branch `main`, so it
matches the branch we push — without it, a repo created while your git still
defaults to `master` would clone with an empty working tree and later `cosm registry
add` couldn't find its `cosm.json`.

**For real use**, swap the local bare repos for repositories on your git host:

| in this tutorial (local) | in real use (e.g. GitHub) |
|---|---|
| `git init --bare -b main "$HOME/remotes/foo.git"` | Create a **new empty repo** in the web UI — with **no** README, license, or `.gitignore` (it must be empty). The host stores it bare for you; you never run `git init --bare`. |
| `file://$HOME/remotes/foo.git` | that repo's clone URL, e.g. `git@github.com:you/foo.git` (SSH is the default) |

Everything else stays identical. First, make a folder to hold the tutorial's local
remotes:

```sh
mkdir -p "$HOME/remotes"
```

## 1. Initialize the depot

```sh
cosm setup
export COSM_DEPOT="$HOME/.cosm"   # if not already set
```

## 2. Create a registry

Create the registry's (empty) remote, then initialize the registry into it:

```sh
git init --bare -b main "$HOME/remotes/cosmlua.git"
cosm registry init cosmlua "file://$HOME/remotes/cosmlua.git"
```

(In real use you'd skip the `git init --bare` line and instead create an empty
`cosmlua` repo on your host, then `cosm registry init cosmlua git@host:you/cosmlua.git`.)

## 3. Write a library

```sh
mkdir strutil && cd strutil
cosm init strutil v0.1.0 --build lua
```

Because you passed `--build lua`, `cosm init` asks the Lua extension to scaffold
the package: it writes `cosm.json` (with `"provides": ["strutil@v0"]` already set),
a source stub at `src/strutil@v0/strutil.lua`, and a `test/` directory. The
`@v0` in both the namespace and the directory is what lets `v0` and a future `v1`
of the module coexist. You only need to fill the stub in:

```sh
cat > src/strutil@v0/strutil.lua <<'LUA'
local strutil = {}

function strutil.greet(name)
  return "Hello, " .. name .. "!"
end

return strutil
LUA
```

## 4. Publish a release

Commit, push to the remote, and cut a release. `cosm release` tags the version
and pushes it:

```sh
# Put the package under version control.
git init && git add . && git commit -m "initial strutil"
git branch -M main

# Create the package's own (empty) remote and push to it.
git init --bare -b main "$HOME/remotes/strutil.git"
git remote add origin "file://$HOME/remotes/strutil.git"
git push -u origin main

# Cut the release: tags v0.1.0 and pushes it.
cosm release v0.1.0
```

(In real use: create an empty `strutil` repo on your host and use
`git@host:you/strutil.git` as the origin instead of the local bare repo.)

## 5. Register it

```sh
cd ..
cosm registry add cosmlua "file://$HOME/remotes/strutil.git"
cosm registry status cosmlua      # shows strutil: v0.1.0
```

## 6. Create an app that uses it

```sh
mkdir greeter && cd greeter
cosm init greeter --build lua
cosm add strutil v0.1.0
```

`cosm init` scaffolds a `src/greeter@v0/greeter.lua` stub for the app too; for a
runnable program we don't need it — just add an entry point. Modules are required
by their `namespace@vMAJOR.module` path:

```sh
cat > src/main.lua <<'LUA'
local strutil = require("strutil@v0.strutil")
print(strutil.greet("World"))
LUA
```

## 7. Build and run

```sh
cosm status      # shows the resolved build list (strutil v0.1.0)
cosm build
cosm run -- lua src/main.lua
# => Hello, World!
```

`cosm run` builds if needed, assembles `LUA_PATH` (your own `src` plus every
dependency's), and runs the command. `cosm env` prints those exports if you want
to load them into your own shell.

## 8. Co-develop the library

Want to change `strutil` while working on `greeter`? Put it in develop mode:

```sh
cosm develop strutil
```

This checks out `strutil` into `$COSM_DEPOT/dev/strutil@v0` and enrolls this
project. Edit `$COSM_DEPOT/dev/strutil@v0/src/strutil@v0/strutil.lua` — e.g. make
`greet` shout — and re-run:

```sh
cosm run -- lua src/main.lua      # reflects your live edits
```

`cosm status` marks `strutil` as `(develop)`. When you're done:

```sh
cosm free strutil
```

## 9. Ship a new version

Back in the library, cut and register a new release, then upgrade the app:

```sh
cd ../strutil
# ...make changes, commit...
cosm release --patch                 # v0.1.1
cd ..
cosm registry add cosmlua "file://$HOME/remotes/strutil.git"   # idempotent: picks up v0.1.1
cd greeter
cosm upgrade strutil                 # raises the floor to v0.1.1
```

That's the whole loop: **init → release → register → add → build → run →
develop → upgrade**.

## Next steps

- The [C++/CMake tutorial](tutorial-cpp.md) does the same for a compiled language.
- The [reference](reference.md) documents every command, flag, and file format.
