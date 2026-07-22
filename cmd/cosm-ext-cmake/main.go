// Command cosm-ext-cmake is the reference C++/CMake build-system extension
// (§9.5). It builds a package with CMake, installs into the prefix, and reports
// an install-prefix descriptor so dependents' find_package(CONFIG) works.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// doTest configures the project's SOURCE fresh with the full test closure on
// CMAKE_PREFIX_PATH (regular deps + testDeps, so find_package of a test-only dep
// like Catch2 succeeds), builds, and runs ctest. It reports pass/fail and the
// number of tests via the response (not a non-zero exit) so the core can surface
// the captured log and guard against a vacuous zero-test pass.
func doTest() {
	var req ext.TestRequest
	must(extlib.ReadRequest(&req))
	if _, err := exec.LookPath("cmake"); err != nil {
		extlib.Fatal("cmake not found on PATH")
	}
	if _, err := exec.LookPath("ctest"); err != nil {
		extlib.Fatal("ctest not found on PATH")
	}
	cfg := buildConfig{BuildType: "Debug"}
	if len(req.Config) > 0 {
		_ = json.Unmarshal(req.Config, &cfg)
	}

	// CMAKE_PREFIX_PATH spans the whole test closure so a test-only dependency's
	// package config is findable at configure time.
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

	tag := sanitize(req.Project.Name + "-" + req.Project.Version)
	logPath := filepath.Join(os.TempDir(), "cosm-test-"+tag+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		extlib.Fatal("open log: %v", err)
	}
	defer logf.Close()
	buildDir := filepath.Join(os.TempDir(), "cosm-cmake-test-"+tag)
	os.RemoveAll(buildDir) // always start clean (avoids stale-cache pinning)
	if !req.KeepBuild {
		defer os.RemoveAll(buildDir)
	}
	kept := ""
	if req.KeepBuild {
		kept = buildDir
	}

	fail := func() {
		must(extlib.WriteResponse(ext.TestResponse{Status: "failed", Log: logPath, Tests: -1, BuildDir: kept}))
	}

	configure := []string{
		"-S", req.Project.Source, "-B", buildDir,
		"-DCMAKE_BUILD_TYPE=" + cfg.BuildType,
		"-DBUILD_TESTING=ON",
	}
	if len(prefixes) > 0 {
		configure = append(configure, "-DCMAKE_PREFIX_PATH="+strings.Join(prefixes, ";"))
	}
	if req.CxxFlags != "" {
		configure = append(configure, "-DCMAKE_CXX_FLAGS="+req.CxxFlags)
	}
	if req.LdFlags != "" {
		configure = append(configure,
			"-DCMAKE_EXE_LINKER_FLAGS="+req.LdFlags,
			"-DCMAKE_SHARED_LINKER_FLAGS="+req.LdFlags)
	}
	if _, cerr := runCapture(logf, "", "cmake", configure...); cerr != nil {
		fail()
		return
	}
	buildArgs := []string{"--build", buildDir}
	if req.Jobs > 0 {
		buildArgs = append(buildArgs, "-j", fmt.Sprintf("%d", req.Jobs))
	}
	if _, berr := runCapture(logf, "", "cmake", buildArgs...); berr != nil {
		fail()
		return
	}

	ctestArgs := []string{}
	if req.Verbose {
		ctestArgs = append(ctestArgs, "-V")
	} else {
		ctestArgs = append(ctestArgs, "--output-on-failure")
	}
	ctestArgs = append(ctestArgs, req.Args...)
	out, terr := runCapture(logf, buildDir, "ctest", ctestArgs...)
	tests := parseCtestCount(out)
	status := "ok"
	if terr != nil {
		status = "failed"
	}
	must(extlib.WriteResponse(ext.TestResponse{Status: status, Log: logPath, Tests: tests, BuildDir: kept}))
}

// runCapture runs name+args in dir (cwd if ""), tee-ing combined output to logf and
// returning it as a string for parsing.
func runCapture(logf *os.File, dir, name string, args ...string) (string, error) {
	fmt.Fprintf(logf, "\n$ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	w := io.MultiWriter(logf, &buf)
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	return buf.String(), err
}

// parseCtestCount extracts the number of tests ctest ran (0 when it found none,
// -1 when it can't be determined).
func parseCtestCount(out string) int {
	if strings.Contains(out, "No tests were found") {
		return 0
	}
	if i := strings.LastIndex(out, "out of "); i >= 0 {
		var n int
		if _, err := fmt.Sscanf(out[i+len("out of "):], "%d", &n); err == nil {
			return n
		}
	}
	return -1
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
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
