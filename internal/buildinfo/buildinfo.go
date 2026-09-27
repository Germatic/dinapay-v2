package buildinfo

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	Service         = "dinapay-v2"
	Repository      = "github.com/Germatic/dinapay-v2"
	ContractVersion = "v2"
)

// These values are overridden by the release build. The Go VCS metadata
// fallback keeps developer builds attributable without a custom build script.
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Info struct {
	Service         string `json:"service"`
	Repository      string `json:"repository"`
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	BuiltAt         string `json:"builtAt"`
	ContractVersion string `json:"contractVersion"`
	GoVersion       string `json:"goVersion"`
	Environment     string `json:"environment"`
}

func Current() Info {
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
	return Info{
		Service: Service, Repository: Repository, Version: version, Commit: commit,
		BuiltAt: builtAt, ContractVersion: ContractVersion, GoVersion: runtime.Version(),
		Environment: environment,
	}
}
