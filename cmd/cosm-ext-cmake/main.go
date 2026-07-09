// Command cosm-ext-cmake is the reference C++/CMake build-system extension
// (§9.5). It builds a package with CMake, installs into the prefix, and reports
// an install-prefix descriptor so dependents' find_package(CONFIG) works.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cosm/internal/ext"
	"cosm/internal/ext/extlib"
	"cosm/internal/semver"
)

const version = "0.1.0"

// cmakeDescriptor is this extension's consumption descriptor.
type cmakeDescriptor struct {
	CMakePrefixPath []string `json:"cmakePrefixPath"`
	LibDirs         []string `json:"libDirs,omitempty"`
	BinDirs         []string `json:"binDirs,omitempty"`
	IncludeDirs     []string `json:"includeDirs,omitempty"`
}

type buildConfig struct {
	BuildType string `json:"buildType"`
}

func main() {
	if len(os.Args) < 2 {
		extlib.Fatal("usage: cosm-ext-cmake <verb>")
	}
	switch os.Args[1] {
	case "info":
		doInfo()
	case "build":
		doBuild()
	case "activate":
		doActivate()
	case "scaffold":
		doScaffold()
	case "test":
		doTest()
	default:
		extlib.Fatal("unknown verb %q", os.Args[1])
	}
}

func doInfo() {
	must(extlib.WriteResponse(ext.Info{
		Extension:    "cmake",
		Version:      version,
		Protocol:     ext.Protocol,
		Languages:    []string{"c++", "c"},
		ToolchainID:  toolchainID(),
		Capabilities: []string{"info", "build", "activate", "scaffold", "test"},
	}))
}

func doBuild() {
	var req ext.BuildRequest
	must(extlib.ReadRequest(&req))
	if _, err := exec.LookPath("cmake"); err != nil {
		extlib.Fatal("cmake not found on PATH")
	}
	cfg := buildConfig{BuildType: "Release"}
	if len(req.Config) > 0 {
		_ = json.Unmarshal(req.Config, &cfg)
	}

	// Collect dependency install prefixes for CMAKE_PREFIX_PATH (descriptors are
	// prefix-relative; join each dep's actual prefix on this machine).
	var prefixes []string
	for _, d := range req.Deps {
		var desc cmakeDescriptor
		if len(d.Descriptor) > 0 {
			_ = json.Unmarshal(d.Descriptor, &desc)
		}
		for _, rel := range desc.CMakePrefixPath {
			prefixes = append(prefixes, filepath.Join(d.Prefix, rel))
		}
	}

	buildDir := filepath.Join(os.TempDir(), "cosm-cmake-"+shortKey(req.BuildKey))
	os.RemoveAll(buildDir)
	defer os.RemoveAll(buildDir)

	configure := []string{
		"-S", req.Package.Source,
		"-B", buildDir,
		"-DCMAKE_INSTALL_PREFIX=" + req.Prefix,
		"-DCMAKE_BUILD_TYPE=" + cfg.BuildType,
	}
	if len(prefixes) > 0 {
		configure = append(configure, "-DCMAKE_PREFIX_PATH="+strings.Join(prefixes, ";"))
	}
	runCMake(configure...)
	jobs := req.Jobs
	if jobs <= 0 {
		jobs = 1
	}
	runCMake("--build", buildDir, "-j", fmt.Sprintf("%d", jobs))
	runCMake("--install", buildDir)

	// Prefix-relative descriptor (relocatable).
	desc := cmakeDescriptor{
		CMakePrefixPath: []string{"."},
		LibDirs:         []string{"lib"},
		BinDirs:         []string{"bin"},
		IncludeDirs:     []string{"include"},
	}
	raw, _ := json.Marshal(desc)
	must(extlib.WriteResponse(ext.BuildResponse{Status: "ok", Descriptor: raw}))
}

func doActivate() {
	var req ext.ActivateRequest
	must(extlib.ReadRequest(&req))
	var prefixPath, libs, bins []string
	for _, d := range req.Deps {
		var desc cmakeDescriptor
		if len(d.Descriptor) > 0 {
			_ = json.Unmarshal(d.Descriptor, &desc)
		}
		for _, rel := range desc.CMakePrefixPath {
			prefixPath = append(prefixPath, filepath.Join(d.Prefix, rel))
		}
		for _, rel := range desc.LibDirs {
			libs = append(libs, filepath.Join(d.Prefix, rel))
		}
		for _, rel := range desc.BinDirs {
			bins = append(bins, filepath.Join(d.Prefix, rel))
		}
	}
	prepend := map[string][]string{}
	if len(prefixPath) > 0 {
		prepend["CMAKE_PREFIX_PATH"] = prefixPath
	}
	if len(libs) > 0 {
		prepend[libEnvVar(req.Platform.OS)] = libs
	}
	if len(bins) > 0 {
		prepend["PATH"] = bins
	}
	must(extlib.WriteResponse(ext.ActivateResponse{PrependPaths: prepend}))
}

func doScaffold() {
	var req ext.ScaffoldRequest
	must(extlib.ReadRequest(&req))
	mj, err := semver.Major(req.Version)
	if err != nil {
		extlib.Fatal("scaffold: %v", err)
	}
	// cosm's namespace is `<name>@v<major>`; its C++ binding is `<name>_v<major>`
	// (`@` is not a legal C++/CMake identifier). The C++ namespace is versioned so
	// two majors of the library can be linked into the same program at once.
	ns := fmt.Sprintf("%s@v%d", req.Name, mj)
	id := fmt.Sprintf("%s_v%d", req.Name, mj)

	cmake := fmt.Sprintf(`cmake_minimum_required(VERSION 3.24)
project(%[1]s LANGUAGES CXX)

add_library(%[1]s src/%[2]s.cpp)
target_compile_features(%[1]s PUBLIC cxx_std_17)
target_include_directories(%[1]s PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
  $<INSTALL_INTERFACE:include>)

install(TARGETS %[1]s EXPORT %[1]sTargets ARCHIVE DESTINATION lib LIBRARY DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
install(EXPORT %[1]sTargets FILE %[1]sConfig.cmake NAMESPACE %[1]s:: DESTINATION lib/cmake/%[1]s)
`, id, req.Name)

	header := fmt.Sprintf("#pragma once\n#include <string>\n\nnamespace %[1]s {\nstd::string hello(const std::string& name);\n}\n", id)
	source := fmt.Sprintf("#include \"%[1]s/%[2]s.hpp\"\n\nnamespace %[3]s {\nstd::string hello(const std::string& name) { return \"Hello, \" + name + \"!\"; }\n}\n", id, req.Name, id)

	files := map[string]string{
		"CMakeLists.txt": cmake,
		filepath.Join("include", id, req.Name+".hpp"): header,
		filepath.Join("src", req.Name+".cpp"):         source,
	}
	var created []string
	for rel, content := range files {
		p := filepath.Join(req.Dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			extlib.Fatal("scaffold: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			extlib.Fatal("scaffold: %v", err)
		}
		created = append(created, rel)
	}
	must(extlib.WriteResponse(ext.ScaffoldResponse{
		Files:    created,
		Provides: []string{ns},
		Ext:      map[string]json.RawMessage{"cmake": json.RawMessage(`{"minimumVersion":"3.24"}`)},
	}))
}

func doTest() {
	var req ext.TestRequest
	must(extlib.ReadRequest(&req))
	_ = req
	must(extlib.WriteResponse(ext.TestResponse{Status: "ok"}))
}

func runCMake(args ...string) {
	cmd := exec.Command("cmake", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr // build logs go to stderr
	if err := cmd.Run(); err != nil {
		extlib.Fatal("cmake %s failed: %v", strings.Join(args, " "), err)
	}
}

func toolchainID() string {
	cc := os.Getenv("CXX")
	if cc == "" {
		cc = "c++"
	}
	out, err := exec.Command(cc, "--version").Output()
	if err != nil {
		return "cxx-unknown"
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.ReplaceAll(line, " ", "-")
}

func libEnvVar(goos string) string {
	if goos == "darwin" {
		return "DYLD_LIBRARY_PATH"
	}
	return "LD_LIBRARY_PATH"
}

func shortKey(k string) string {
	k = strings.TrimPrefix(k, "sha256:")
	if len(k) > 16 {
		return k[:16]
	}
	return k
}

func must(err error) {
	if err != nil {
		extlib.Fatal("%v", err)
	}
}
