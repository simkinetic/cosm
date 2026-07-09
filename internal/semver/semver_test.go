package semver

import "testing"

func TestCompareAndMax(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.2.0", 0},
		{"v1.2.0", "v1.10.0", -1},
		{"v1.10.0", "v1.2.0", 1},
		{"v1.0.0-alpha", "v1.0.0", -1}, // prerelease precedes release
		{"v2.0.0", "v1.9.9", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if Max("v1.2.0", "v1.3.0") != "v1.3.0" {
		t.Errorf("Max picked lower")
	}
	if Max("v1.3.0", "v1.3.0") != "v1.3.0" {
		t.Errorf("Max tie should return first")
	}
}

func TestMajor(t *testing.T) {
	for _, c := range []struct {
		v    string
		want int
	}{{"v0.6.4", 0}, {"v1.2.3", 1}, {"v12.0.0", 12}, {"v2.0.0-rc.1", 2}} {
		got, err := Major(c.v)
		if err != nil || got != c.want {
			t.Errorf("Major(%q)=%d,%v want %d", c.v, got, err, c.want)
		}
	}
	if _, err := Major("nonsense"); err == nil {
		t.Error("expected error for invalid version")
	}
}

func TestValidateExact(t *testing.T) {
	ok := []string{"v1.2.3", "v0.0.1", "v1.2.3-alpha", "v1.2.3+build.5"}
	for _, v := range ok {
		if err := ValidateExact(v); err != nil {
			t.Errorf("ValidateExact(%q) unexpected error: %v", v, err)
		}
	}
	bad := []string{"v1", "v1.2", "1.2.3", "vx.y.z", ""}
	for _, v := range bad {
		if err := ValidateExact(v); err == nil {
			t.Errorf("ValidateExact(%q) expected error", v)
		}
	}
}

func TestUnitKeyRoundTrip(t *testing.T) {
	key := UnitKey("d38cb13b-f524", 0)
	if key != "d38cb13b-f524@v0" {
		t.Fatalf("UnitKey = %q", key)
	}
	uuid, major, err := SplitUnitKey(key)
	if err != nil || uuid != "d38cb13b-f524" || major != 0 {
		t.Fatalf("SplitUnitKey(%q) = %q,%d,%v", key, uuid, major, err)
	}
	for _, bad := range []string{"noatsign", "@v1", "uuid@1", "uuid@vx"} {
		if _, _, err := SplitUnitKey(bad); err == nil {
			t.Errorf("SplitUnitKey(%q) expected error", bad)
		}
	}
}
