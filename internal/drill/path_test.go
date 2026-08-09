package drill

import "testing"

func TestStripWindowsFileURLPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		goos string
		want string
	}{
		{"windows drive-letter path is stripped", "/C:/backups/x.dump", "windows", "C:/backups/x.dump"},
		{"windows path too short to match is left alone", "/x", "windows", "/x"},
		{
			// This is the exact bug in finding #9: a Linux user pastes a
			// Windows-style file:///C:/... path and previously got it
			// silently mangled into a bogus relative path.
			"linux absolute path shaped like a drive letter is left alone",
			"/C:/backups/x.dump", "linux", "/C:/backups/x.dump",
		},
		{"darwin absolute path left alone", "/var/backups/x.dump", "darwin", "/var/backups/x.dump"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripWindowsFileURLPath(tc.in, tc.goos)
			if got != tc.want {
				t.Errorf("stripWindowsFileURLPath(%q, %q) = %q, want %q", tc.in, tc.goos, got, tc.want)
			}
		})
	}
}
