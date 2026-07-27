package cli

import "testing"

func TestVersionStatus(t *testing.T) {
	cases := []struct {
		name    string
		version string
		repoSha string
		want    string
	}{
		{name: "unstamped dev build is not outdated", version: "dev", repoSha: "abc123", want: "unstamped"},
		{name: "empty version is unstamped", version: "", repoSha: "abc123", want: "unstamped"},
		{name: "unstamped without repo sha", version: "dev", repoSha: "", want: "unstamped"},
		{name: "stamped matching head", version: "abc123", repoSha: "abc123", want: "latest"},
		{name: "stamped behind head", version: "def456", repoSha: "abc123", want: "outdated"},
		{name: "stamped but repo sha unavailable", version: "abc123", repoSha: "", want: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionStatus(tc.version, tc.repoSha); got != tc.want {
				t.Fatalf("versionStatus(%q, %q) = %q, want %q", tc.version, tc.repoSha, got, tc.want)
			}
		})
	}
}
