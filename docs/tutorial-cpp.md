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

We use `file://` URLs as remotes for the tutorial; in real use they'd be
`git@host:org/repo.git`.

## 1. Initialize the depot and a registry

```sh
cosm setup
git init --bare "$HOME/remotes/cosmcpp.git"
cosm registry init cosmcpp "file://$HOME/remotes/cosmcpp.git"
```

## 2. Write a library

```sh
mkdir greet && cd greet
cosm init greet v0.1.0 --build cmake
```

Add a header, a source file, and a `CMakeLists.txt` that installs an **exported**
target so consumers can `find_package(greet CONFIG)`:

```sh
mkdir -p include/greet src

cat > include/greet/greet.hpp <<'HPP'
#pragma once
#include <string>
namespace greet { std::string hello(const std::string& name); }
HPP

cat > src/greet.cpp <<'CPP'
#include "greet/greet.hpp"
namespace greet { std::string hello(const std::string& name) { return "Hello, " + name + "!"; } }
CPP

cat > CMakeLists.txt <<'CMAKE'
cmake_minimum_required(VERSION 3.24)
project(greet LANGUAGES CXX)

add_library(greet src/greet.cpp)
target_compile_features(greet PUBLIC cxx_std_17)
target_include_directories(greet PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
  $<INSTALL_INTERFACE:include>)

install(TARGETS greet EXPORT greetTargets ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT greetTargets
  FILE greetConfig.cmake
  NAMESPACE greet::
  DESTINATION lib/cmake/greet)
CMAKE
```

The CMake extension builds the package with `CMAKE_INSTALL_PREFIX` set to a
content-addressed location in the depot, and reports that prefix so dependents'
`find_package` can locate it.

## 3. Release and register

```sh
git init && git add . && git commit -m "initial greet"
git branch -M main
git remote add origin "file://$HOME/remotes/greet.git"   # a bare repo you created
git push -u origin main
cosm release v0.1.0

cd ..
cosm registry add cosmcpp "file://$HOME/remotes/greet.git"
```

## 4. Create an app that links the library

```sh
mkdir hello && cd hello
cosm init hello --build cmake
cosm add greet v0.1.0

mkdir -p src
cat > src/main.cpp <<'CPP'
#include <iostream>
#include "greet/greet.hpp"
int main() {
  std::cout << greet::hello("World") << std::endl;
  return 0;
}
CPP

cat > CMakeLists.txt <<'CMAKE'
cmake_minimum_required(VERSION 3.24)
project(hello LANGUAGES CXX)

find_package(greet CONFIG REQUIRED)

add_executable(hello src/main.cpp)
target_link_libraries(hello PRIVATE greet::greet)

install(TARGETS hello RUNTIME DESTINATION bin)
CMAKE
```

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
