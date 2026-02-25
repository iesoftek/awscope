package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if info, ok := debug.ReadBuildInfo(); ok && info != nil {
				fmt.Printf("awscope %s\n", info.Main.Version)
				var (
					rev      string
					vcsTime  string
					modified string
				)
				for _, s := range info.Settings {
					switch s.Key {
					case "vcs.revision":
						rev = s.Value
					case "vcs.time":
						vcsTime = s.Value
					case "vcs.modified":
						modified = s.Value
					}
				}
				if rev != "" {
					fmt.Printf("revision: %s\n", rev)
				}
				if vcsTime != "" {
					fmt.Printf("time: %s\n", vcsTime)
				}
				if modified != "" {
					fmt.Printf("modified: %s\n", modified)
				}
				if info.GoVersion != "" {
					fmt.Printf("go: %s\n", info.GoVersion)
				}
				return nil
			}
			fmt.Println("awscope dev")
			return nil
		},
	}
}
