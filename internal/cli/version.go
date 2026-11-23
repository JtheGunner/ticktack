package cli

import (
	"runtime/debug"

	"github.com/spf13/cobra"
)

// defaultVersion is the value of Version when no release build set it.
const defaultVersion = "dev"

// maxRevisionLength is how many characters of a VCS revision are shown.
const maxRevisionLength = 12

// resolveVersion returns the ldflags version when a release build set one.
// Otherwise (a `go install` or plain `go build`) it falls back to the module
// version from the build info, then to the VCS revision, then to the default.
func resolveVersion(ldflags string, info *debug.BuildInfo) string {
	if ldflags != defaultVersion || info == nil {
		return ldflags
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	revision, modified := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return ldflags
	}
	if len(revision) > maxRevisionLength {
		revision = revision[:maxRevisionLength]
	}
	if modified {
		revision += "-dirty"
	}
	return defaultVersion + "-" + revision
}

// currentVersion is the version this binary reports.
func currentVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	return resolveVersion(Version, info)
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: msgVersionVersionShort,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte("ticktack " + currentVersion() + "\n"))
			return err
		},
	}
}
