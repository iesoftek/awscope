package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"awscope/internal/core"
	"awscope/internal/graph"
	"awscope/internal/security"
	"awscope/internal/securityui"
	"awscope/internal/store"

	"github.com/spf13/cobra"
)

func newSecurityCmd(dbPath *string) *cobra.Command {
	var (
		profile       string
		regions       string
		services      string
		maxKeyAgeDays int
		view          string
		colorMode     string
		tuiMode       bool
	)

	cmd := &cobra.Command{
		Use:   "security",
		Short: "Analyze potential security issues from cached inventory",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()

			profile = strings.TrimSpace(profile)
			if profile == "" {
				return fmt.Errorf("--profile is required")
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			meta, ok, err := st.LookupProfile(runCtx, profile)
			if err != nil {
				return err
			}
			if !ok || strings.TrimSpace(meta.AccountID) == "" {
				return fmt.Errorf("unknown profile %q in DB (run `awscope scan --profile %s ...` first)", profile, profile)
			}
			accountID := strings.TrimSpace(meta.AccountID)

			selectedRegions, err := resolveSecurityRegions(runCtx, st, accountID, regions)
			if err != nil {
				return err
			}

			explicitServices := normalizeServices(parseCSV(services))
			serviceScope := explicitServices
			var latestRun store.ScanRunMeta
			var latestOK bool
			if len(serviceScope) == 0 {
				latestRun, latestOK, err = st.GetLatestSuccessfulScanRunByProfile(runCtx, profile)
				if err != nil {
					return err
				}
				if latestOK && len(latestRun.ProviderIDs) > 0 {
					serviceScope = normalizeServices(latestRun.ProviderIDs)
				}
			}

			nodes, err := st.ListResourceNodesByAccountAndScope(runCtx, accountID, selectedRegions, serviceScope)
			if err != nil {
				return err
			}

			scannedServices := serviceScope
			if len(scannedServices) == 0 {
				scannedServices = deriveServicesFromNodes(nodes)
			}

			secRaw := security.Evaluate(security.EvaluateInput{
				Nodes:           nodes,
				SelectedRegions: selectedRegions,
				ScannedServices: scannedServices,
				MaxKeyAgeDays:   maxKeyAgeDays,
			})
			sec := core.ScanSecuritySummaryFromEvaluator(secRaw)
			showDetails, err := parseSecurityDetailView(view)
			if err != nil {
				return err
			}
			colorEnabled, err := resolveColorEnabled(colorMode, cmd.OutOrStdout())
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "security report: profile=%s account=%s resources=%d db=%s\n",
				profile, accountID, len(nodes), st.DBPath())
			if latestOK {
				when := "-"
				if !latestRun.EndedAt.IsZero() {
					when = latestRun.EndedAt.UTC().Format(time.RFC3339)
				}
				if tuiMode {
					if err := securityui.Run(runCtx, securityui.Options{
						Profile:       profile,
						AccountID:     accountID,
						ResourceCount: len(nodes),
						DBPath:        st.DBPath(),
						SourceLine:    fmt.Sprintf("latest successful scan id=%s ended_at=%s", latestRun.ScanID, when),
						Summary:       sec,
						ShowDetails:   showDetails,
						Color:         colorEnabled,
					}); err != nil {
						return err
					}
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  source: latest successful scan id=%s ended_at=%s\n", latestRun.ScanID, when)
			} else if tuiMode {
				if err := securityui.Run(runCtx, securityui.Options{
					Profile:       profile,
					AccountID:     accountID,
					ResourceCount: len(nodes),
					DBPath:        st.DBPath(),
					Summary:       sec,
					ShowDetails:   showDetails,
					Color:         colorEnabled,
				}); err != nil {
					return err
				}
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), formatSecuritySummaryWithOptions(sec, securitySummaryFormatOptions{
				ShowDetails: showDetails,
				Color:       colorEnabled,
			}))
			return nil
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (required; scopes to profile-mapped account)")
	cmd.Flags().StringVar(&regions, "regions", "all", "Comma-separated regions to analyze (default: all account regions in DB)")
	cmd.Flags().StringVar(&services, "services", "", "Optional comma-separated service IDs to scope security analysis")
	cmd.Flags().IntVar(&maxKeyAgeDays, "max-key-age-days", intEnvOr("AWSCOPE_SECURITY_MAX_KEY_AGE_DAYS", 90), "Threshold for stale active IAM access keys in days")
	cmd.Flags().StringVar(&view, "view", "detailed", "Output detail view: summary|detailed")
	cmd.Flags().StringVar(&colorMode, "color", "auto", "Output color mode: auto|always|never")
	cmd.Flags().BoolVar(&tuiMode, "tui", false, "Open an interactive security TUI viewer")
	return cmd
}

func resolveSecurityRegions(ctx context.Context, st *store.Store, accountID string, regionFlag string) ([]string, error) {
	regionFlag = strings.TrimSpace(regionFlag)
	var regions []string
	if regionFlag == "" || strings.EqualFold(regionFlag, "all") {
		rs, err := st.ListDistinctRegions(ctx, accountID)
		if err != nil {
			return nil, err
		}
		regions = rs
	} else {
		regions = parseCSV(regionFlag)
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(regions)+1)
	for _, r := range regions {
		r = strings.TrimSpace(strings.ToLower(r))
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	if _, ok := seen["global"]; !ok {
		out = append(out, "global")
	}
	return out, nil
}

func normalizeServices(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, svc := range in {
		svc = strings.TrimSpace(strings.ToLower(svc))
		if svc == "" {
			continue
		}
		if _, ok := seen[svc]; ok {
			continue
		}
		seen[svc] = struct{}{}
		out = append(out, svc)
	}
	sort.Strings(out)
	return out
}

func deriveServicesFromNodes(nodes []graph.ResourceNode) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 16)
	for _, n := range nodes {
		svc := strings.TrimSpace(strings.ToLower(n.Service))
		if svc == "" {
			continue
		}
		if _, ok := seen[svc]; ok {
			continue
		}
		seen[svc] = struct{}{}
		out = append(out, svc)
	}
	sort.Strings(out)
	return out
}
