package main

import (
	"fmt"

	"awscope/internal/buildinfo"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := buildinfo.Read()
			fmt.Printf("awscope %s\n", info.Version)
			if info.Commit != "" {
				fmt.Printf("revision: %s\n", info.Commit)
			}
			if info.Date != "" {
				fmt.Printf("time: %s\n", info.Date)
			}
			if info.Modified != "" {
				fmt.Printf("modified: %s\n", info.Modified)
			}
			if info.GoVersion != "" {
				fmt.Printf("go: %s\n", info.GoVersion)
			}
			return nil
		},
	}
}
