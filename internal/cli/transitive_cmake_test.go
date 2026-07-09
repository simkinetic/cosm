package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTransitive_Cmake is the acceptance test for the transitive-prefix fix: a
// 3-level chain app -> mid -> leaf where mid's installed config calls
// find_dependency(leaf_v0). Building app must expose leaf's install prefix (a
// transitive dep) on CMAKE_PREFIX_PATH, or find_dependency fails. Verified for
// both registry-resolved and `cosm develop` resolution.
func TestTransitive_Cmake(t *testing.T) {
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not installed")
	}
	if !hasCxxCompiler() {
		t.Skip("no C++ compiler")
	}

	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, out)
		}
		return out
	}
	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ok(runCLI(t, home, "setup"))
	buildExtInto(t, filepath.Join(home, ".cosm"), "cmake")
	ok(runCLI(t, home, "registry", "init", "cosmt", bare(t, home, "cosmt.git")))

	// publish creates a package, writes its files (over the scaffold), records deps,
	// releases it, and registers it.
	publish := func(name, ver string, files map[string]string, deps [][2]string) {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		ok(runCLI(t, dir, "init", name, ver, "--build", "cmake"))
		for rel, content := range files {
			writeFile(filepath.Join(dir, rel), content)
		}
		for _, d := range deps {
			ok(runCLI(t, dir, "add", d[0], d[1]))
		}
		remote := bare(t, home, name+".git")
		gitRun(t, dir, "init")
		gitRun(t, dir, "add", ".")
		gitRun(t, dir, "commit", "-m", "init")
		gitRun(t, dir, "branch", "-M", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")
		ok(runCLI(t, dir, "release", ver))
		ok(runCLI(t, home, "registry", "add", "cosmt", remote))
	}

	// leaf: header-only INTERFACE library, no deps.
	publish("leaf", "v0.1.0", map[string]string{
		"include/leaf_v0/leaf.hpp": "#pragma once\nnamespace leaf_v0 { inline int val() { return 42; } }\n",
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.24)
project(leaf_v0 LANGUAGES CXX)
add_library(leaf_v0 INTERFACE)
target_include_directories(leaf_v0 INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include> $<INSTALL_INTERFACE:include>)
install(TARGETS leaf_v0 EXPORT leaf_v0Targets)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT leaf_v0Targets FILE leaf_v0Config.cmake NAMESPACE leaf_v0:: DESTINATION lib/cmake/leaf_v0)
`,
	}, nil)

	// mid: depends on leaf; its installed config wraps find_dependency(leaf_v0).
	publish("mid", "v0.1.0", map[string]string{
		"include/mid_v0/mid.hpp": "#pragma once\n#include \"leaf_v0/leaf.hpp\"\nnamespace mid_v0 { inline int val() { return leaf_v0::val() + 1; } }\n",
		"cmake/mid_v0Config.cmake.in": `@PACKAGE_INIT@
include(CMakeFindDependencyMacro)
find_dependency(leaf_v0)
include("${CMAKE_CURRENT_LIST_DIR}/mid_v0Targets.cmake")
`,
		"CMakeLists.txt": `cmake_minimum_required(VERSION 3.24)
project(mid_v0 LANGUAGES CXX)
find_package(leaf_v0 CONFIG REQUIRED)
add_library(mid_v0 INTERFACE)
target_link_libraries(mid_v0 INTERFACE leaf_v0::leaf_v0)
target_include_directories(mid_v0 INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include> $<INSTALL_INTERFACE:include>)
install(TARGETS mid_v0 EXPORT mid_v0Targets)
install(DIRECTORY include/ DESTINATION include)
include(CMakePackageConfigHelpers)
configure_package_config_file(${CMAKE_CURRENT_SOURCE_DIR}/cmake/mid_v0Config.cmake.in
  ${CMAKE_CURRENT_BINARY_DIR}/mid_v0Config.cmake INSTALL_DESTINATION lib/cmake/mid_v0)
install(FILES ${CMAKE_CURRENT_BINARY_DIR}/mid_v0Config.cmake DESTINATION lib/cmake/mid_v0)
install(EXPORT mid_v0Targets FILE mid_v0Targets.cmake NAMESPACE mid_v0:: DESTINATION lib/cmake/mid_v0)
`,
	}, [][2]string{{"leaf", "v0.1.0"}})

	// app depends on mid only; leaf is transitive.
	app := filepath.Join(home, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, app, "init", "app", "--build", "cmake"))
	writeFile(filepath.Join(app, "src", "main.cpp"),
		"#include <iostream>\n#include \"mid_v0/mid.hpp\"\nint main() { std::cout << \"val=\" << mid_v0::val() << std::endl; return 0; }\n")
	writeFile(filepath.Join(app, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.24)
project(app LANGUAGES CXX)
find_package(mid_v0 CONFIG REQUIRED)
add_executable(app src/main.cpp)
target_link_libraries(app PRIVATE mid_v0::mid_v0)
install(TARGETS app RUNTIME DESTINATION bin)
`)
	ok(runCLI(t, app, "add", "mid", "v0.1.0"))

	// leaf must be in the resolved closure even though it's not a direct dep.
	if out := ok(runCLI(t, app, "status")); !strings.Contains(out, "leaf") {
		t.Fatalf("status omitted transitive leaf: %q", out)
	}

	// Registry-resolved build+run: fails pre-fix at find_dependency(leaf_v0).
	ok(runCLI(t, app, "build"))
	if out := ok(runCLI(t, app, "run", "--", "app")); !strings.Contains(out, "val=43") {
		t.Fatalf("run output = %q, want val=43", out)
	}

	// Develop the transitive dep and rebuild: the closure is still forwarded.
	ok(runCLI(t, app, "develop", "leaf"))
	ok(runCLI(t, app, "build"))
	if out := ok(runCLI(t, app, "run", "--", "app")); !strings.Contains(out, "val=43") {
		t.Fatalf("develop run output = %q, want val=43", out)
	}
}
