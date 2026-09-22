package buildinfo

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// Values are overridden at build time with -ldflags. The debug build metadata
// fallback keeps local and go-install builds identifiable without special tooling.
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Info struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuiltAt     string `json:"builtAt"`
	GoVersion   string `json:"goVersion"`
	Environment string `json:"environment"`
}

func Current(service string) Info {
	version, commit, builtAt := Version, Commit, BuiltAt
	if bi, ok := debug.ReadBuildInfo(); ok {
		if version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "unknown" && setting.Value != "" {
					commit = setting.Value
				}
			case "vcs.time":
				if builtAt == "unknown" && setting.Value != "" {
					builtAt = setting.Value
				}
			case "vcs.modified":
				if setting.Value == "true" && commit != "unknown" && !strings.HasSuffix(commit, "+dirty") {
					commit += "+dirty"
				}
			}
		}
	}
	environment := strings.TrimSpace(os.Getenv("DINARIA_ENVIRONMENT"))
	if environment == "" {
		environment = "unknown"
	}
	return Info{Service: service, Version: version, Commit: commit, BuiltAt: builtAt, GoVersion: runtime.Version(), Environment: environment}
}
