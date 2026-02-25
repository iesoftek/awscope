package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"awscope/internal/store"

	"github.com/spf13/cobra"
)

func newExportCmd(dbPath *string) *cobra.Command {
	var (
		format  string
		out     string
		profile string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export inventory from SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			profile = strings.TrimSpace(profile)
			format = strings.TrimSpace(strings.ToLower(format))
			if format == "" {
				format = "json"
			}

			// Resolve output file name if not provided.
			if out == "" {
				label := "all"
				if profile != "" {
					label = profile
				}
				label = sanitizeFilename(label)
				ts := time.Now().Format("20060102-150405")
				ext := format
				out = fmt.Sprintf("awscope-export-%s-%s.%s", label, ts, ext)
			}

			accountID := ""
			if profile != "" {
				meta, ok, err := st.LookupProfile(runCtx, profile)
				if err != nil {
					return err
				}
				if !ok || strings.TrimSpace(meta.AccountID) == "" {
					return fmt.Errorf("unknown profile %q in DB (run `awscope scan --profile %s ...` first)", profile, profile)
				}
				accountID = meta.AccountID
			}

			doLogSuccess := func() {
				dst := out
				if abs, err := filepath.Abs(out); err == nil {
					dst = abs
				}
				scope := "all"
				if profile != "" {
					scope = profile
				}
				fmt.Fprintf(cmd.OutOrStdout(), "export complete: format=%s scope=%s file=%s\n", format, scope, dst)
			}

			switch format {
			case "json":
				if err := exportJSON(runCtx, st, out, accountID); err != nil {
					return err
				}
				doLogSuccess()
				return nil
			case "csv":
				if err := exportCSV(runCtx, st, out, profile, accountID); err != nil {
					return err
				}
				doLogSuccess()
				return nil
			default:
				return fmt.Errorf("unsupported --format %q (supported: json,csv)", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "json", "Export format (json,csv)")
	cmd.Flags().StringVar(&out, "out", "", "Output file path (optional; default: ./awscope-export-<profile|all>-<timestamp>.<ext>)")
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (optional; when set, export only resources for that profile/account)")
	return cmd
}

func exportJSON(ctx context.Context, st *store.Store, outPath string, accountID string) error {
	var (
		snap store.ExportSnapshot
		err  error
	)
	if strings.TrimSpace(accountID) != "" {
		snap, err = st.ExportLatestByAccount(ctx, accountID)
	} else {
		snap, err = st.ExportLatest(ctx)
	}
	if err != nil {
		return err
	}
	return store.WriteJSONFile(outPath, snap)
}

func exportCSV(ctx context.Context, st *store.Store, outPath string, profile string, accountID string) error {
	includeProfileCol := strings.TrimSpace(profile) == ""

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return st.ExportResourcesCSV(ctx, f, store.ExportResourcesCSVOptions{
		AccountID:            accountID,
		IncludeProfileColumn: includeProfileCol,
	})
}
