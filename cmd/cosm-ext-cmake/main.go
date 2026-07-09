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
	files := map[string]string{
		"cosm.json": fmt.Sprintf("{\n  \"schemaVersion\": 1,\n  \"name\": %q,\n  \"uuid\": %q,\n  \"version\": %q,\n  \"build\": \"cmake\",\n  \"provides\": [%q],\n  \"ext\": { \"cmake\": { \"minimumVersion\": \"3.24\" } }\n}\n",
			req.Name, req.UUID, req.Version, req.Name),
		"CMakeLists.txt": fmt.Sprintf("cmake_minimum_required(VERSION 3.24)\nproject(%s LANGUAGES CXX)\n\nadd_library(%s src/%s.cpp)\ntarget_include_directories(%s PUBLIC $<INSTALL_INTERFACE:include>)\n\ninstall(TARGETS %s EXPORT %sTargets ARCHIVE DESTINATION lib LIBRARY DESTINATION lib)\ninstall(EXPORT %sTargets DESTINATION lib/cmake/%s FILE %sConfig.cmake)\n",
			req.Name, req.Name, req.Name, req.Name, req.Name, req.Name, req.Name, req.Name, req.Name),
		filepath.Join("src", req.Name+".cpp"): "// " + req.Name + "\n",
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
	must(extlib.WriteResponse(ext.ScaffoldResponse{Files: created}))
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
