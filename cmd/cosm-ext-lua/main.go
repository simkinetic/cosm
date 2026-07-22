// Command cosm-ext-lua is the reference Lua build-system extension (§9.4).
// Lua is interpreted: "build" copies sources into the prefix and the descriptor
// carries LUA_PATH roots; "activate" assembles LUA_PATH from deps + the project.
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

// luaDescriptor is this extension's consumption descriptor.
type luaDescriptor struct {
	LuaPath  []string `json:"luaPath"`
	LuaCPath []string `json:"luaCPath,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		extlib.Fatal("usage: cosm-ext-lua <verb>")
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
		Extension:    "lua",
		Version:      version,
		Protocol:     ext.Protocol,
		Languages:    []string{"lua"},
		ToolchainID:  "lua-interpreted",
		Capabilities: []string{"info", "build", "activate", "scaffold", "test"},
	}))
}

func doBuild() {
	var req ext.BuildRequest
	must(extlib.ReadRequest(&req))
	// "Install" = copy the source tree into the prefix.
	if err := extlib.CopyTree(req.Package.Source, req.Prefix); err != nil {
		extlib.Fatal("copy sources: %v", err)
	}
	// Descriptor paths are prefix-relative so the artifact is relocatable.
	desc := luaDescriptor{LuaPath: relRoots()}
	raw, _ := json.Marshal(desc)
	must(extlib.WriteResponse(ext.BuildResponse{
		Status:     "ok",
		Descriptor: raw,
		Artifacts:  []string{"src"},
	}))
}

func doActivate() {
	var req ext.ActivateRequest
	must(extlib.ReadRequest(&req))
	var paths []string
	for _, d := range req.Deps {
		var desc luaDescriptor
		if len(d.Descriptor) > 0 {
			_ = json.Unmarshal(d.Descriptor, &desc)
		}
		for _, rel := range desc.LuaPath {
			paths = append(paths, filepath.Join(d.Prefix, rel))
		}
	}
	// The project's own sources come first.
	proj := luaRoots(req.Project.Source)
	value := strings.Join(append(proj, paths...), ";") + ";;"
	must(extlib.WriteResponse(ext.ActivateResponse{
		Env: map[string]string{"LUA_PATH": value},
	}))
}

func doScaffold() {
	var req ext.ScaffoldRequest
	must(extlib.ReadRequest(&req))
	// The namespace carries the major so v0 and a future v1 can coexist; it also
	// names the source directory the consumer requires ("<ns>.<module>").
	mj, err := semver.Major(req.Version)
	if err != nil {
		extlib.Fatal("scaffold: %v", err)
	}
	ns := fmt.Sprintf("%s@v%d", req.Name, mj)
	files := map[string]string{
		filepath.Join("src", ns, req.Name+".lua"): "local M = {}\n\nreturn M\n",
		filepath.Join("test", "test.lua"):         "-- tests for " + req.Name + "\n",
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
	must(extlib.WriteResponse(ext.ScaffoldResponse{Files: created, Provides: []string{ns}}))
}

// doTest runs each test/*.lua with LUA_PATH assembled from the test closure. It
// skips (reports unknown) when no interpreter is installed, so the pipeline stays
// testable without lua; otherwise a non-zero test is a failure and the number of
// test files is reported so the core can guard a zero-test run.
func doTest() {
	var req ext.TestRequest
	must(extlib.ReadRequest(&req))
	if _, err := exec.LookPath("lua"); err != nil {
		must(extlib.WriteResponse(ext.TestResponse{Status: "ok", Tests: -1}))
		return
	}
	files, _ := filepath.Glob(filepath.Join(req.Project.Source, "test", "*.lua"))

	roots := luaRoots(req.Project.Source)
	for _, d := range req.Deps {
		var desc luaDescriptor
		if len(d.Descriptor) > 0 {
			_ = json.Unmarshal(d.Descriptor, &desc)
		}
		for _, rel := range desc.LuaPath {
			roots = append(roots, filepath.Join(d.Prefix, rel))
		}
	}
	luaPath := strings.Join(roots, ";") + ";;"

	logPath := filepath.Join(os.TempDir(), "cosm-test-"+sanitize(req.Project.Name)+".log")
	logf, ferr := os.Create(logPath)
	if ferr != nil {
		extlib.Fatal("open log: %v", ferr)
	}
	defer logf.Close()

	status := "ok"
	for _, f := range files {
		fmt.Fprintf(logf, "\n$ lua %s\n", f)
		cmd := exec.Command("lua", f)
		cmd.Env = append(os.Environ(), "LUA_PATH="+luaPath)
		var buf bytes.Buffer
		w := io.MultiWriter(logf, &buf)
		cmd.Stdout, cmd.Stderr = w, w
		if err := cmd.Run(); err != nil {
			status = "failed"
		}
	}
	must(extlib.WriteResponse(ext.TestResponse{Status: status, Log: logPath, Tests: len(files)}))
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

// luaRoots returns the absolute LUA_PATH "?" roots for a package rooted at dir.
func luaRoots(dir string) []string {
	return []string{
		filepath.Join(dir, "src", "?.lua"),
		filepath.Join(dir, "src", "?", "init.lua"),
	}
}

// relRoots returns the prefix-relative LUA_PATH "?" roots (for descriptors).
func relRoots() []string {
	return []string{
		filepath.Join("src", "?.lua"),
		filepath.Join("src", "?", "init.lua"),
	}
}

func must(err error) {
	if err != nil {
		extlib.Fatal("%v", err)
	}
}
