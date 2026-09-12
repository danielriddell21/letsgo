package bytesize

import "testing"

func TestConstants(t *testing.T) {
	for _, c := range []struct {
		got  Size
		want int64
	}{
		{B, 1},
		{KB, 1024},
		{MB, 1024 * 1024},
		{GB, 1024 * 1024 * 1024},
		{TB, 1024 * 1024 * 1024 * 1024},
	} {
		if int64(c.got) != c.want {
			t.Errorf("got %d, want %d", int64(c.got), c.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Size
	}{
		{"0", 0},
		{"900", 900},
		{"900B", 900},
		{"1KB", KB},
		{"1k", KB},
		{"1KiB", KB},
		{"15MB", 15 * MB},
		{"15 mb", 15 * MB},
		{"  15MiB  ", 15 * MB},
		{"1.5GB", 3 * GB / 2},
		{"2TB", 2 * TB},
	} {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "MB", "fifteen", "15 megabytes", "-1MB", "1.2.3KB"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %d, want an error", in, got)
		}
	}
}

func TestString(t *testing.T) {
	for _, c := range []struct {
		in   Size
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{KB, "1.0 KB"},
		{1536, "1.5 KB"},
		{5 * MB, "5.0 MB"},
		{3 * GB, "3.0 GB"},
		{2 * TB, "2.0 TB"},
		{2048 * TB, "2048.0 TB"},
		{-1536, "-1.5 KB"},
	} {
		if got := c.in.String(); got != c.want {
			t.Errorf("Size(%d).String() = %q, want %q", int64(c.in), got, c.want)
		}
	}
}
