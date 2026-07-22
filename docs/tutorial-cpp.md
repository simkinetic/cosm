# Tutorial: a C++ project with cosm and CMake

This walks through publishing a C++ **library** and consuming it from an **app**,
using the CMake extension. cosm resolves and orders the build; CMake does the
compiling and `find_package` wiring.

Everything here is exercised by an integration test
(`internal/cli/tutorial_cpp_test.go`), so the commands are known to work.

## Prerequisites

- `cosm`, `cosm-ext-cmake` on your `PATH` (or install the extension under
  `$COSM_DEPOT/extensions/cmake/cosm-ext-cmake`). See the [README](../README.md).
- `cmake` (≥ 3.24) and a C++ compiler on your `PATH`.
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

## 1. Initialize the depot and a registry

```sh
cosm setup

# Create the registry's (empty) remote, then initialize the registry into it.
git init --bare -b main "$HOME/remotes/cosmcpp.git"
cosm registry init cosmcpp "file://$HOME/remotes/cosmcpp.git"
```

(In real use you'd skip the `git init --bare` line and instead create an empty
`cosmcpp` repo on your host, then `cosm registry init cosmcpp git@host:you/cosmcpp.git`.)

## 2. Write a library

```sh
mkdir greet && cd greet
cosm init greet v0.1.0 --build cmake
```

Because you passed `--build cmake`, `cosm init` asks the CMake extension to
scaffold the package: it sets `provides: ["greet@v0"]` in `cosm.json` (plus a
default `ext.cmake.minimumVersion`) and writes a compiling starter — a
`CMakeLists.txt`, `include/greet_v0/greet.hpp`, and `src/greet.cpp`.

**The `greet_v0` spelling.** cosm's compatibility unit is `greet@v0`. Since `@` is
not a legal C++/CMake identifier, its C++ binding is `greet_v0` — used for the
include directory, the C++ namespace, and the CMake package/target. The namespace
is versioned on purpose: `greet@v0` and a future `greet@v1` become
`greet_v0::hello` and `greet_v1::hello`, so a program can link **both majors at
once** and migrate call sites one at a time instead of all in a single flag day.

The scaffold already compiles; a real `greet` looks like:

```sh
cat > include/greet_v0/greet.hpp <<'HPP'
#pragma once
#include <string>
namespace greet_v0 { std::string hello(const std::string& name); }
HPP

cat > src/greet.cpp <<'CPP'
#include "greet_v0/greet.hpp"
namespace greet_v0 { std::string hello(const std::string& name) { return "Hello, " + name + "!"; } }
CPP

cat > CMakeLists.txt <<'CMAKE'
cmake_minimum_required(VERSION 3.24)
project(greet_v0 LANGUAGES CXX)

add_library(greet_v0 src/greet.cpp)
target_compile_features(greet_v0 PUBLIC cxx_std_17)
target_include_directories(greet_v0 PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
  $<INSTALL_INTERFACE:include>)

install(TARGETS greet_v0 EXPORT greet_v0Targets ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT greet_v0Targets
  FILE greet_v0Config.cmake
  NAMESPACE greet_v0::
  DESTINATION lib/cmake/greet_v0)

# Tests: gated on BUILD_TESTING so a plain `cosm build` skips them; `cosm test`
# configures with -DBUILD_TESTING=ON.
if(BUILD_TESTING)
  enable_testing()
  add_executable(test_greet test/test_greet.cpp)
  target_link_libraries(test_greet PRIVATE greet_v0)
  add_test(NAME greet_hello COMMAND test_greet)
endif()
CMAKE
```

The CMake extension builds the package with `CMAKE_INSTALL_PREFIX` set to a
content-addressed location in the depot, and reports that prefix so dependents'
`find_package` can locate it.

### Test it

Write a test and run it with `cosm test`. cosm builds the project, configures with
`-DBUILD_TESTING=ON`, and runs `ctest` — **failing** if a test fails or if *no* tests
are discovered (so a mis-gated test can't pass vacuously):

```sh
cat > test/test_greet.cpp <<'CPP'
#include "greet_v0/greet.hpp"
// Return non-zero on failure (works in Release, where assert() is a no-op).
int main() { return greet_v0::hello("cosm") == "Hello, cosm!" ? 0 : 1; }
CPP

cosm test
# => Tests ok (1 run)
```

`cosm test -- <args>` forwards to `ctest` (e.g. `cosm test -- -R greet_hello
--output-on-failure`), `--verbose` prints the full output, and `--cxxflags`/`--ldflags`
inject per-run flags (e.g. coverage instrumentation).

For a real test framework, add it as a **test-only** dependency:

```sh
cosm add catch2 --test        # once catch2 is in a registry
```

A `--test` dependency is on `CMAKE_PREFIX_PATH` for `cosm test` (so
`find_package(Catch2 CONFIG)` resolves) but is **not** inherited by anyone who depends
on `greet` — test frameworks don't leak to your consumers.

## 3. Release and register

```sh
# Put the package under version control.
git init && git add . && git commit -m "initial greet"
git branch -M main

# Create the package's own (empty) remote and push to it.
git init --bare -b main "$HOME/remotes/greet.git"
git remote add origin "file://$HOME/remotes/greet.git"
git push -u origin main

# Cut the release (tags v0.1.0 and pushes it), then register it.
cosm release v0.1.0
cd ..
cosm registry add cosmcpp "file://$HOME/remotes/greet.git"
```

(In real use: create an empty `greet` repo on your host, use
`git@host:you/greet.git` as the origin, and register it with that same URL.)

## 4. Create an app that links the library

```sh
mkdir hello && cd hello
cosm init hello --build cmake
cosm add greet v0.1.0

cat > src/main.cpp <<'CPP'
#include <iostream>
#include "greet_v0/greet.hpp"
int main() {
  std::cout << greet_v0::hello("World") << std::endl;
  return 0;
}
CPP

cat > CMakeLists.txt <<'CMAKE'
cmake_minimum_required(VERSION 3.24)
project(hello LANGUAGES CXX)

find_package(greet_v0 CONFIG REQUIRED)

add_executable(hello src/main.cpp)
target_link_libraries(hello PRIVATE greet_v0::greet_v0)

install(TARGETS hello RUNTIME DESTINATION bin)
CMAKE
```

(You reference the library by its C++ binding `greet_v0` — the same
`find_package` name, include prefix, and `::` namespace. To adopt a later major,
add `greet@v1` as a separate dependency and switch call sites to `greet_v1::`
incrementally; both can be linked at the same time.)

## 5. Build and run

```sh
cosm status      # resolved build list: greet v0.1.0
cosm build       # builds greet, then hello (topological order)
cosm run -- hello
# => Hello, World!
```

`cosm build` builds each dependency in dependency order, passing each one's
install prefix to the next via `CMAKE_PREFIX_PATH`. `cosm run` puts the built
binary on `PATH`; `cosm env` prints the exports (`CMAKE_PREFIX_PATH`,
`DYLD_LIBRARY_PATH`/`LD_LIBRARY_PATH`, `PATH`) for your own shell.

## 6. Binary distribution (optional)

In a corporate setting some users may have binary-only access. Publish a prebuilt
artifact to a binary (or mixed) registry and consumers materialize it with no
source build:

```sh
# from the greet package, after building:
git init --bare -b main "$HOME/remotes/cosmbin.git"     # the binary registry's empty remote
cosm registry init cosmbin "file://$HOME/remotes/cosmbin.git" --kind mixed
cosm publish --registry cosmbin --store "$HOME/artifacts"
```

`--store` can be omitted if you set a default once with
`cosm setup --store "$HOME/artifacts"` (recorded in `config.json`).

A consumer that can reach `cosmbin` (and the artifact store) resolves and uses the
binary; access is gated purely by registry/store permissions.

## Next steps

- The [Lua tutorial](tutorial-lua.md) does the same for an interpreted language.
- The [reference](reference.md) documents every command, flag, and file format.
