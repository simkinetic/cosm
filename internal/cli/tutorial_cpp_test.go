package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTutorial_Cpp encodes docs/tutorial-cpp.md end to end: publish a C++
// library, link it from an app, build with CMake, and run the binary. Skips if
// cmake or a C++ compiler is unavailable.
func TestTutorial_Cpp(t *testing.T) {
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
	ok(runCLI(t, home, "registry", "init", "cosmcpp", bare(t, home, "cosmcpp.git")))

	// Library `greet`.
	lib := filepath.Join(home, "greet")
	os.MkdirAll(lib, 0o755)
	ok(runCLI(t, lib, "init", "greet", "v0.1.0", "--build", "cmake"))
	writeFile(filepath.Join(lib, "include", "greet_v0", "greet.hpp"),
		"#pragma once\n#include <string>\nnamespace greet_v0 { std::string hello(const std::string& name); }\n")
	writeFile(filepath.Join(lib, "src", "greet.cpp"),
		"#include \"greet_v0/greet.hpp\"\nnamespace greet_v0 { std::string hello(const std::string& name) { return \"Hello, \" + name + \"!\"; } }\n")
	writeFile(filepath.Join(lib, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.24)
project(greet_v0 LANGUAGES CXX)
add_library(greet_v0 src/greet.cpp)
target_compile_features(greet_v0 PUBLIC cxx_std_17)
target_include_directories(greet_v0 PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
  $<INSTALL_INTERFACE:include>)
install(TARGETS greet_v0 EXPORT greet_v0Targets ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT greet_v0Targets FILE greet_v0Config.cmake NAMESPACE greet_v0:: DESTINATION lib/cmake/greet_v0)
`)

	greetRemote := bare(t, home, "greet.git")
	gitRun(t, lib, "init")
	gitRun(t, lib, "add", ".")
	gitRun(t, lib, "commit", "-m", "initial greet")
	gitRun(t, lib, "branch", "-M", "main")
	gitRun(t, lib, "remote", "add", "origin", greetRemote)
	gitRun(t, lib, "push", "-u", "origin", "main")
	ok(runCLI(t, lib, "release", "v0.1.0"))
	ok(runCLI(t, home, "registry", "add", "cosmcpp", greetRemote))

	// App `hello`.
	app := filepath.Join(home, "hello")
	os.MkdirAll(app, 0o755)
	ok(runCLI(t, app, "init", "hello", "--build", "cmake"))
	ok(runCLI(t, app, "add", "greet", "v0.1.0"))
	writeFile(filepath.Join(app, "src", "main.cpp"),
		"#include <iostream>\n#include \"greet_v0/greet.hpp\"\nint main() { std::cout << greet_v0::hello(\"World\") << std::endl; return 0; }\n")
	writeFile(filepath.Join(app, "CMakeLists.txt"), `cmake_minimum_required(VERSION 3.24)
project(hello LANGUAGES CXX)
find_package(greet_v0 CONFIG REQUIRED)
add_executable(hello src/main.cpp)
target_link_libraries(hello PRIVATE greet_v0::greet_v0)
install(TARGETS hello RUNTIME DESTINATION bin)
`)

	if out := ok(runCLI(t, app, "status")); !strings.Contains(out, "greet v0.1.0") {
		t.Errorf("status: %q", out)
	}
	ok(runCLI(t, app, "build"))
	out := ok(runCLI(t, app, "run", "--", "hello"))
	if !strings.Contains(out, "Hello, World!") {
		t.Fatalf("program output = %q, want 'Hello, World!'", out)
	}
}

func hasCxxCompiler() bool {
	for _, c := range []string{"c++", "g++", "clang++"} {
		if _, err := exec.LookPath(c); err == nil {
			return true
		}
	}
	return false
}
