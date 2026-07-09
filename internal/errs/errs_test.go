package errs

import (
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{ErrUsage, 2},
		{fmt.Errorf("wrapped: %w", ErrUsage), 2},
		{ErrRegistryNotFound, 3},
		{ErrPackageNotFound, 3},
		{ErrExtNotFound, 3},
		{fmt.Errorf("ctx: %w", ErrPackageNotFound), 3},
		{ErrNetwork, 4},
		{ErrIntegrityMismatch, 5},
		{ErrTagMoved, 5},
		{&BuildFailedError{Package: "x", Phase: "cmake", LogPath: "/l"}, 6},
		{fmt.Errorf("ctx: %w", &BuildFailedError{Package: "x"}), 6},
		{ErrInternal, 70},
		{fmt.Errorf("some random error"), 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("ExitCode(%v)=%d want %d", c.err, got, c.want)
		}
	}
}
