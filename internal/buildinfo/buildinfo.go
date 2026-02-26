package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Values injected at build time via -ldflags.
var (
	Version string
	Commit  string
	Date    string
)

type Info struct {
	Version   string
	Commit    string
	Date      string
	Modified  string
	GoVersion string
}

func Read() Info {
	info := Info{}

	if bi, ok := debug.ReadBuildInfo(); ok && bi != nil {
		info.Version = normalizeVersion(bi.Main.Version)
		info.GoVersion = strings.TrimSpace(bi.GoVersion)

		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Commit = strings.TrimSpace(s.Value)
			case "vcs.time":
				info.Date = strings.TrimSpace(s.Value)
			case "vcs.modified":
				info.Modified = strings.TrimSpace(s.Value)
			}
		}
	}

	if v := normalizeVersion(Version); v != "" {
		info.Version = v
	}
	if c := strings.TrimSpace(Commit); c != "" {
		info.Commit = c
	}
	if d := strings.TrimSpace(Date); d != "" {
		info.Date = d
	}

	if info.Version == "" {
		info.Version = "dev"
	}

	return info
}

func Label() string {
	if v := normalizeVersion(Version); v != "" {
		return v
	}

	info := Read()
	if c := strings.TrimSpace(info.Commit); c != "" {
		if len(c) > 12 {
			return c[:12]
		}
		return c
	}

	if v := strings.TrimSpace(info.Version); v != "" {
		return v
	}

	return "dev"
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return ""
	}
	return v
}
