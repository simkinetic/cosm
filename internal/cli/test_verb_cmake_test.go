// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cosm/internal/errs"
)

// TestCmakeTest_TestDepsAndGuardrail is the regression for the vacuous-pass bug:
// a test-only dependency's prefix must reach the `cosm test` configure step (so a
// test target gated on find_package(<testdep>) is actually built), a failing test
// must fail `cosm test`, and a run that discovers zero tests must not pass.
func TestCmakeTest_TestDepsAndGuardrail(t *testing.T) {
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not installed")
	}
	if _, err := exec.LookPath("ctest"); err != nil {
		t.Skip("ctest not installed")
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
	ok(runCLI(t, home, "registry", "init", "R", bare(t, home, "reg.git")))

	// checklib: a header-only INTERFACE library used only in tests (a Catch2 stand-in).
	lib := filepath.Join(home, "checklib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, lib, "init", "checklib", "v0.1.0", "--build", "cmake"))
	writeFile(filepath.Join(lib, "include", "checklib_v0", "checklib.hpp"),
		"#pragma once\nnamespace checklib_v0 { inline bool check(bool c){ return c; } }\n")
	writeFile(filepath.Join(lib, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.24)
project(checklib_v0 LANGUAGES CXX)
add_library(checklib_v0 INTERFACE)
target_include_directories(checklib_v0 INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include> $<INSTALL_INTERFACE:include>)
install(TARGETS checklib_v0 EXPORT checklib_v0Targets)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT checklib_v0Targets FILE checklib_v0Config.cmake NAMESPACE checklib_v0:: DESTINATION lib/cmake/checklib_v0)
`)
	libRemote := bare(t, home, "checklib.git")
	gitRun(t, lib, "init")
	gitRun(t, lib, "add", ".")
	gitRun(t, lib, "commit", "-m", "init")
	gitRun(t, lib, "branch", "-M", "main")
	gitRun(t, lib, "remote", "add", "origin", libRemote)
	gitRun(t, lib, "push", "-u", "origin", "main")
	ok(runCLI(t, lib, "release", "v0.1.0"))
	ok(runCLI(t, home, "registry", "add", "R", libRemote))

	// widget: depends on checklib only for tests; its test target is gated on
	// find_package(checklib_v0) — skipped by a plain build, enabled under test.
	app := filepath.Join(home, "widget")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, app, "init", "widget", "v0.1.0", "--build", "cmake"))
	ok(runCLI(t, app, "add", "checklib", "v0.1.0", "--test"))
	writeFile(filepath.Join(app, "include", "widget_v0", "widget.hpp"),
		"#pragma once\nnamespace widget_v0 { inline int answer(){ return 42; } }\n")
	writeCanary := func(expected int) {
		writeFile(filepath.Join(app, "test", "test_widget.cpp"),
			"#include \"widget_v0/widget.hpp\"\n#include \"checklib_v0/checklib.hpp\"\n"+
				"int main(){ return checklib_v0::check(widget_v0::answer() == "+strconv.Itoa(expected)+") ? 0 : 1; }\n")
	}
	writeCanary(42) // passing
	writeFile(filepath.Join(app, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.24)
project(widget_v0 LANGUAGES CXX)
add_library(widget_v0 INTERFACE)
target_include_directories(widget_v0 INTERFACE
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include> $<INSTALL_INTERFACE:include>)
install(TARGETS widget_v0 EXPORT widget_v0Targets)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT widget_v0Targets FILE widget_v0Config.cmake NAMESPACE widget_v0:: DESTINATION lib/cmake/widget_v0)

find_package(checklib_v0 CONFIG QUIET)
if(checklib_v0_FOUND)
  enable_testing()
  add_executable(test_widget test/test_widget.cpp)
  target_link_libraries(test_widget PRIVATE widget_v0 checklib_v0::checklib_v0)
  add_test(NAME widget_canary COMMAND test_widget)
endif()
`)

	// Plain build succeeds (no testDep prefix; test target skipped).
	ok(runCLI(t, app, "build"))

	// Passing test: the testDep prefix reaches configure, the test builds and passes.
	if out := ok(runCLI(t, app, "test")); !strings.Contains(out, "Tests ok") {
		t.Fatalf("expected passing tests, got: %q", out)
	}

	// Wishlist: extra compile/link flags reach the test configure (coverage can ride
	// cosm test), and `env --expand` emits no unexpanded ${VAR}.
	ok(runCLI(t, app, "test", "--cxxflags", "-DNDEBUG=1", "--ldflags", ""))
	if out := ok(runCLI(t, app, "env", "--expand")); strings.Contains(out, "${") {
		t.Fatalf("env --expand left an unexpanded ${VAR}: %q", out)
	}

	// Wishlist: --keep-build retains the test build tree and prints its path (so
	// coverage tools can find the test binary after the run).
	out := ok(runCLI(t, app, "test", "--keep-build"))
	kept := ""
	for _, ln := range strings.Split(out, "\n") {
		if s := strings.TrimPrefix(ln, "Build tree kept at "); s != ln {
			kept = strings.TrimSpace(s)
		}
	}
	if kept == "" {
		t.Fatalf("--keep-build did not report a build tree: %q", out)
	}
	if _, err := os.Stat(filepath.Join(kept, "CMakeCache.txt")); err != nil {
		t.Fatalf("kept build tree missing CMakeCache.txt: %v", err)
	}
	os.RemoveAll(kept)

	// A deliberately failing test must fail `cosm test` — reported as a TEST failure
	// (not "build failed …"), with the test exit code (6).
	writeCanary(43)
	_, err := runCLI(t, app, "test")
	if err == nil {
		t.Fatal("a failing test must make 'cosm test' fail")
	}
	if !strings.Contains(err.Error(), "tests failed") || strings.Contains(err.Error(), "build failed") {
		t.Fatalf("test failure should read as a test failure, got: %v", err)
	}
	if code := errs.ExitCode(err); code != 6 {
		t.Fatalf("test-failure exit code = %d, want 6", code)
	}

	// Zero-test guardrail: without the testDep, find_package fails, no tests are
	// configured — a zero-test run must not pass ("Tests ok (0 run)" is impossible),
	// and it fails with the same test exit code.
	ok(runCLI(t, app, "rm", "checklib"))
	_, err = runCLI(t, app, "test")
	if err == nil || !strings.Contains(err.Error(), "no tests were discovered") {
		t.Fatalf("zero-test run must fail with a no-tests message, got: %v", err)
	}
	if code := errs.ExitCode(err); code != 6 {
		t.Fatalf("zero-test exit code = %d, want 6", code)
	}
}
