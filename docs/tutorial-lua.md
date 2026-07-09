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

For the tutorial we use a local directory as a "remote" via `file://` URLs; in
real use these would be `git@host:org/repo.git`.

## 1. Initialize the depot

```sh
cosm setup
export COSM_DEPOT="$HOME/.cosm"   # if not already set
```

## 2. Create a registry

A registry is just a git repo. Create an empty bare repo to act as its remote,
then initialize it:

```sh
git init --bare "$HOME/remotes/cosmlua.git"
cosm registry init cosmlua "file://$HOME/remotes/cosmlua.git"
```

## 3. Write a library

```sh
mkdir strutil && cd strutil
cosm init strutil v0.1.0 --build lua
```

`cosm.json` now describes the package. Add a source module under
`src/<namespace>@v<major>/` — the `@v0` directory is what lets `v0` and a future
`v1` of the module coexist:

```sh
mkdir -p src/strutil@v0
cat > src/strutil@v0/strutil.lua <<'LUA'
local strutil = {}

function strutil.greet(name)
  return "Hello, " .. name .. "!"
end

return strutil
LUA
```

Declare the module namespace in `cosm.json` (`"provides": ["strutil@v0"]`).

## 4. Publish a release

Commit, push to the remote, and cut a release. `cosm release` tags the version
and pushes it:

```sh
git init && git add . && git commit -m "initial strutil"
git branch -M main
git remote add origin "file://$HOME/remotes/strutil.git"   # a bare repo you created
git push -u origin main
cosm release v0.1.0
```

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

Write the program. Modules are required by their `namespace@vMAJOR.module` path:

```sh
mkdir -p src
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
cosm registry add cosmlua strutil v0.1.1
cd ../greeter
cosm upgrade strutil                 # raises the floor to v0.1.1
```

That's the whole loop: **init → release → register → add → build → run →
develop → upgrade**. See the [C++/CMake tutorial](tutorial-cpp.md) for a compiled
language.
