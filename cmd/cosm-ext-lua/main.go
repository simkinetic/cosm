// Command cosm-ext-lua is the reference Lua build-system extension (§9.4).
// Lua is interpreted: "build" copies sources into the prefix and the descriptor
// carries LUA_PATH roots; "activate" assembles LUA_PATH from deps + the project.
package main

import (
	"encoding/json"
	"fmt"
	"os"
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

func doTest() {
	var req ext.TestRequest
	must(extlib.ReadRequest(&req))
	// A full runner would exec `lua test/...`; the reference keeps it a no-op OK
	// so the pipeline is testable without a lua interpreter installed.
	must(extlib.WriteResponse(ext.TestResponse{Status: "ok"}))
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
