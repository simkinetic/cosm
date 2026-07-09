package semver

import "testing"

func TestParseAndString(t *testing.T) {
	v, err := Parse("v1.2.3")
	if err != nil || v.Major != 1 || v.Minor != 2 || v.Patch != 3 {
		t.Fatalf("Parse: %+v %v", v, err)
	}
	if v.String() != "v1.2.3" {
		t.Errorf("String = %q", v.String())
	}
	if _, err := Parse("v1.2"); err == nil {
		t.Error("Parse should reject partial")
	}
	// prerelease/build core parses.
	if v, err := Parse("v2.0.0-rc.1+build.9"); err != nil || v.Major != 2 {
		t.Fatalf("Parse prerelease: %+v %v", v, err)
	}
}

func TestIsValidAndMaxEdge(t *testing.T) {
	if !IsValid("v0.0.1") || IsValid("nope") {
		t.Error("IsValid wrong")
	}
	if Max("v1.0.0", "v1.0.0-rc.1") != "v1.0.0" {
		t.Error("release should beat prerelease")
	}
}
