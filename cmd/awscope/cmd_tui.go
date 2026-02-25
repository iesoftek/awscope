package main

import (
	"context"

	"awscope/internal/store"
	"awscope/internal/tui"

	"github.com/spf13/cobra"
)

func newTuiCmd(ctx context.Context, dbPath *string, offline *bool) *cobra.Command {
	var profile string
	var iconsMode string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Run the interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(store.OpenOptions{
				Path:    *dbPath,
				Offline: *offline,
			})
			if err != nil {
				return err
			}
			defer st.Close()

			return tui.Run(ctx, st, tui.Options{Profile: profile, Icons: iconsMode})
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (defaults to AWS_PROFILE or 'default')")
	cmd.Flags().StringVar(&iconsMode, "icons", "", "Icons mode: ascii|nerd|none (overrides AWSCOPE_ICONS; default: nerd)")
	return cmd
}
