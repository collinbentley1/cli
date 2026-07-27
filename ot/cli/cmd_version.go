package cli

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

var Version = "dev"

// versionStatus classifies the running binary against the repo HEAD.
// A plain `go build` leaves Version unstamped ("dev"); that is not
// "outdated" — there is no stamp to compare — so it reports as
// "unstamped" and skips the upgrade nag. install.sh stamps the git SHA
// via -ldflags.
func versionStatus(version, repoSha string) string {
	switch {
	case version == "" || version == "dev":
		return "unstamped"
	case repoSha == "":
		return "unknown"
	case version == repoSha:
		return "latest"
	default:
		return "outdated"
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print ot CLI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			repoSha := ""
			if out, err := run.RunCapture(cmd.Context(), run.Spec{
				Program: "git",
				Args:    []string{"-C", rt.RepoRoot, "rev-parse", "HEAD"},
			}); err == nil {
				repoSha = strings.TrimSpace(out)
			}

			status := versionStatus(Version, repoSha)
			fmt.Printf("ot version %s (%s/%s) [%s]\n", Version, runtime.GOOS, runtime.GOARCH, status)
			switch status {
			case "outdated":
				fmt.Println("Run `ot upgrade` to update.")
			case "unstamped":
				fmt.Println("Built without a version stamp (plain `go build`); ot/scripts/install.sh stamps the git SHA.")
			}
			return nil
		},
	}
}
