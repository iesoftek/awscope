package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/aws"
	"awscope/internal/core"
	"awscope/internal/diagram"
	"awscope/internal/graph"
	"awscope/internal/providers/registry"
	"awscope/internal/scanui"
	"awscope/internal/store"
	"awscope/internal/tui"

	_ "awscope/internal/actions/ec2"
	_ "awscope/internal/actions/ecs"

	_ "awscope/internal/providers/accessanalyzer"
	_ "awscope/internal/providers/acm"
	_ "awscope/internal/providers/apigateway"
	_ "awscope/internal/providers/autoscaling"
	_ "awscope/internal/providers/cloudfront"
	_ "awscope/internal/providers/cloudtrail"
	_ "awscope/internal/providers/config"
	_ "awscope/internal/providers/dynamodb"
	_ "awscope/internal/providers/ec2"
	_ "awscope/internal/providers/ecr"
	_ "awscope/internal/providers/ecs"
	_ "awscope/internal/providers/efs"
	_ "awscope/internal/providers/eks"
	_ "awscope/internal/providers/elasticache"
	_ "awscope/internal/providers/elbv2"
	_ "awscope/internal/providers/guardduty"
	_ "awscope/internal/providers/iam"
	_ "awscope/internal/providers/identitycenter"
	_ "awscope/internal/providers/kms"
	_ "awscope/internal/providers/lambda"
	_ "awscope/internal/providers/logs"
	_ "awscope/internal/providers/msk"
	_ "awscope/internal/providers/opensearch"
	_ "awscope/internal/providers/rds"
	_ "awscope/internal/providers/redshift"
	_ "awscope/internal/providers/s3"
	_ "awscope/internal/providers/sagemaker"
	_ "awscope/internal/providers/secretsmanager"
	_ "awscope/internal/providers/securityhub"
	_ "awscope/internal/providers/sns"
	_ "awscope/internal/providers/sqs"
	_ "awscope/internal/providers/wafv2"

	"github.com/spf13/cobra"
)

func main() {
	ctx := context.Background()

	var (
		dbPath  string
		offline bool
	)

	root := &cobra.Command{
		Use:           "awscope",
		Short:         "AWS resource browser TUI + inventory",
		SilenceErrors: true, // main() prints the error once for consistent formatting.
	}
	root.PersistentFlags().StringVar(&dbPath, "db-path", "", "Path to SQLite database (default: platform-specific user data dir)")
	root.PersistentFlags().BoolVar(&offline, "offline", false, "Disable AWS calls; browse cached inventory only")

	root.AddCommand(newTuiCmd(ctx, &dbPath, &offline))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newScanCmd(&dbPath, &offline))
	root.AddCommand(newDiagramCmd(&dbPath))
	root.AddCommand(newExportCmd(&dbPath))
	root.AddCommand(newCacheCmd())
	root.AddCommand(newActionCmd(&dbPath, &offline))

	// Default to TUI if no subcommand is provided.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return newTuiCmd(ctx, &dbPath, &offline).RunE(cmd, args)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

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

func newScanCmd(dbPath *string, offline *bool) *cobra.Command {
	var (
		profile             string
		regions             string
		services            string
		plain               bool
		concurrency         int
		resolverConcurrency int
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan AWS and populate the local inventory",
		// For runtime scan errors (e.g. AccessDenied), don't spam help text.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()

			if offline != nil && *offline {
				return fmt.Errorf("--offline is not compatible with scan")
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			regionList := parseCSV(regions)
			if len(regionList) == 0 {
				return fmt.Errorf("--regions is required (comma-separated, e.g. us-east-1,us-west-2, or 'all')")
			}
			if len(regionList) == 1 && strings.EqualFold(regionList[0], "all") {
				// Discover enabled regions using EC2 DescribeRegions.
				loader := aws.NewLoader()
				cfg, _, err := loader.Load(runCtx, profile, "us-east-1")
				if err != nil {
					return err
				}
				discovered, err := loader.ListEnabledRegions(runCtx, cfg)
				if err != nil {
					return fmt.Errorf("discover regions: %w (provide explicit --regions to bypass)", err)
				}
				regionList = discovered
			}

			serviceIDs := parseCSV(services)
			if len(serviceIDs) == 0 {
				// Default to scanning all registered providers.
				serviceIDs = registry.ListIDs()
				sort.Strings(serviceIDs)
			}
			// Validate early for a clearer error.
			for _, sid := range serviceIDs {
				if _, ok := registry.Get(sid); !ok {
					return fmt.Errorf("unknown service/provider %q (known: %v)", sid, registry.ListIDs())
				}
			}

			app := core.New(st)
			var res core.ScanResult
			if plain {
				res, err = app.Scan(runCtx, core.ScanOptions{
					Profile:             profile,
					Regions:             regionList,
					ProviderIDs:         serviceIDs,
					MaxConcurrency:      concurrency,
					ResolverConcurrency: resolverConcurrency,
				})
			} else {
				res, err = scanui.Run(runCtx, st, scanui.Options{
					Profile:             profile,
					Regions:             regionList,
					ProviderIDs:         serviceIDs,
					MaxConcurrency:      concurrency,
					ResolverConcurrency: resolverConcurrency,
				})
			}
			if err != nil {
				return err
			}

			fmt.Printf("scan complete: account=%s partition=%s resources=%d edges=%d db=%s\n",
				res.AccountID, res.Partition, res.Resources, res.Edges, st.DBPath())
			if plain && len(res.StepFailures) > 0 {
				fmt.Printf("errors (%d):\n", len(res.StepFailures))
				for i, f := range res.StepFailures {
					if i >= 50 {
						fmt.Printf("  ... (+%d more)\n", len(res.StepFailures)-i)
						break
					}
					label := strings.TrimSpace(fmt.Sprintf("%s %s %s", f.Phase, f.ProviderID, f.Region))
					fmt.Printf("  - %-32s  %s\n", label, strings.TrimSpace(f.Error))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (uses default chain if empty)")
	cmd.Flags().StringVar(&regions, "regions", "", "Comma-separated regions to scan (required; supports 'all')")
	servicesUsage := "Comma-separated service/provider IDs to scan (default: all supported)"
	if known := registry.ListIDs(); len(known) > 0 {
		sort.Strings(known)
		servicesUsage += fmt.Sprintf(" (supported: %s)", strings.Join(known, ","))
	}
	cmd.Flags().StringVar(&services, "services", "", servicesUsage)
	cmd.Flags().BoolVar(&plain, "plain", false, "Disable progress UI and print only the final summary")
	cmd.Flags().IntVar(&concurrency, "concurrency", 8, "Max concurrent AWS scan tasks")
	cmd.Flags().IntVar(&resolverConcurrency, "resolver-concurrency", 4, "Max concurrent resolver tasks (ELBv2 membership)")
	return cmd
}

func parseCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func newDiagramCmd(dbPath *string) *cobra.Command {
	var (
		profile             string
		region              string
		format              string
		outDir              string
		name                string
		full                bool
		view                string
		includeGlobalLinked bool
		includeIsolated     string
		layout              string
		noFold              bool
		componentLimit      int
		render              bool
		maxNodes            int
		maxEdges            int
	)
	cmd := &cobra.Command{
		Use:   "diagram",
		Short: "Generate architecture diagram(s) from scanned inventory",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			if strings.TrimSpace(region) == "" {
				return fmt.Errorf("--region is required")
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			accountID, err := resolveDiagramAccount(runCtx, st, profile)
			if err != nil {
				return err
			}

			resources, err := st.ListResourcesByAccountAndRegion(runCtx, accountID, region)
			if err != nil {
				return err
			}

			keys := make([]graph.ResourceKey, 0, len(resources))
			seenKeys := map[string]struct{}{}
			for _, r := range resources {
				s := string(r.Key)
				if _, ok := seenKeys[s]; ok {
					continue
				}
				seenKeys[s] = struct{}{}
				keys = append(keys, r.Key)
			}

			edges, err := st.ListEdgesByResourceKeys(runCtx, keys)
			if err != nil {
				return err
			}

			var warnings []string
			if includeGlobalLinked {
				globalResources, crossEdges, err := st.ListLinkedGlobalResourcesForRegion(runCtx, accountID, region)
				if err != nil {
					return err
				}
				if len(globalResources) > 0 {
					warnings = append(warnings, fmt.Sprintf("included %d linked global resource(s)", len(globalResources)))
				}
				for _, gr := range globalResources {
					if _, ok := seenKeys[string(gr.Key)]; ok {
						continue
					}
					seenKeys[string(gr.Key)] = struct{}{}
					resources = append(resources, gr)
					keys = append(keys, gr.Key)
				}
				allEdges, err := st.ListEdgesByResourceKeys(runCtx, keys)
				if err != nil {
					return err
				}
				edges = dedupeEdges(append(append(edges, crossEdges...), allEdges...))
			}

			scope := diagram.Scope{
				AccountID:           accountID,
				Region:              region,
				IncludeGlobalLinked: includeGlobalLinked,
			}
			viewSel, err := diagram.ParseView(view)
			if err != nil {
				return err
			}
			if full {
				viewSel = diagram.ViewFull
			}
			layoutSel, err := diagram.ParseLayout(layout)
			if err != nil {
				return err
			}
			if layoutSel == diagram.LayoutSFDP && viewSel != diagram.ViewFull {
				return fmt.Errorf("--layout sfdp is only supported with --view full (or --full)")
			}
			includeIsolatedSel, err := diagram.ParseIncludeIsolated(includeIsolated)
			if err != nil {
				return err
			}

			scope.View = viewSel
			scope.Full = viewSel == diagram.ViewFull
			scope.IncludeIsolated = includeIsolatedSel
			scope.Layout = string(layoutSel)

			model := diagram.BuildModel(scope, resources, edges)
			model = diagram.ProcessModel(model, diagram.ProcessOptions{
				View:            viewSel,
				MaxNodes:        maxNodes,
				MaxEdges:        maxEdges,
				ComponentLimit:  componentLimit,
				IncludeIsolated: includeIsolatedSel,
				NoFold:          noFold,
			})
			if model.Scope.Full && maxNodes == 0 && maxEdges == 0 {
				model.OmittedNodes = 0
				model.OmittedEdges = 0
			}
			if model.OmittedNodes > 0 || model.OmittedEdges > 0 {
				warnings = append(warnings, fmt.Sprintf("diagram output omitted %d node(s), %d edge(s)", model.OmittedNodes, model.OmittedEdges))
			}
			if len(model.Nodes) == 0 {
				warnings = append(warnings, "no resources found for selected scope")
			}

			mode, err := parseDiagramFormat(format)
			if err != nil {
				return err
			}

			outDir = strings.TrimSpace(outDir)
			if outDir == "" {
				outDir = "."
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}

			base := strings.TrimSpace(name)
			if base == "" {
				base = fmt.Sprintf("awscope-diagram-%s-%s-%s", sanitizeFilename(accountID), sanitizeFilename(region), time.Now().Format("20060102-150405"))
			} else {
				base = sanitizeFilename(base)
			}

			var files []string
			writeSource := func(ext string, content []byte) (string, error) {
				p := filepath.Join(outDir, base+ext)
				if err := os.WriteFile(p, content, 0o644); err != nil {
					return "", err
				}
				files = append(files, p)
				return p, nil
			}

			if mode == diagramFormatGraphviz || mode == diagramFormatBoth {
				content, err := (diagram.GraphvizRenderer{}).Render(model)
				if err != nil {
					return err
				}
				dotPath, err := writeSource(".dot", content)
				if err != nil {
					return err
				}
				if render {
					svgPath := filepath.Join(outDir, diagramSVGName(base, mode, "graphviz"))
					if w := renderGraphviz(dotPath, svgPath, model.Scope.Layout); w != "" {
						warnings = append(warnings, w)
					} else {
						files = append(files, svgPath)
					}
				}
			}

			if mode == diagramFormatMermaid || mode == diagramFormatBoth {
				content, err := (diagram.MermaidRenderer{}).Render(model)
				if err != nil {
					return err
				}
				mmdPath, err := writeSource(".mmd", content)
				if err != nil {
					return err
				}
				if render {
					svgPath := filepath.Join(outDir, diagramSVGName(base, mode, "mermaid"))
					if w := renderMermaid(mmdPath, svgPath); w != "" {
						warnings = append(warnings, w)
					} else {
						files = append(files, svgPath)
					}
				}
			}

			for _, w := range warnings {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", w)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "diagram complete: account=%s region=%s files=%s\n", accountID, region, strings.Join(files, ", "))
			return nil
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (optional; scopes to profile-mapped account)")
	cmd.Flags().StringVar(&region, "region", "", "AWS region to diagram (required)")
	cmd.Flags().StringVar(&format, "format", "graphviz", "Output format: graphviz|mermaid|both")
	cmd.Flags().StringVar(&outDir, "out-dir", ".", "Output directory")
	cmd.Flags().StringVar(&name, "name", "", "Base output filename (optional)")
	cmd.Flags().BoolVar(&full, "full", false, "Alias for --view full")
	cmd.Flags().StringVar(&view, "view", string(diagram.ViewOverview), "Diagram projection: overview|network|eventing|security|full")
	cmd.Flags().StringVar(&includeIsolated, "include-isolated", string(diagram.IncludeIsolatedSummary), "How to handle isolated nodes: summary|full|none")
	cmd.Flags().StringVar(&layout, "layout", string(diagram.LayoutDot), "Layout engine: dot|sfdp (sfdp only for view=full)")
	cmd.Flags().BoolVar(&noFold, "no-fold", false, "Disable leaf and parallel-edge folding")
	cmd.Flags().IntVar(&componentLimit, "component-limit", 3, "Keep top N disconnected components in non-full views")
	cmd.Flags().BoolVar(&includeGlobalLinked, "include-global-linked", true, "Include global resources directly linked to selected region resources")
	cmd.Flags().BoolVar(&render, "render", true, "Render source to SVG when dot/mmdc is available")
	cmd.Flags().IntVar(&maxNodes, "max-nodes", 0, "Max nodes (0 uses view default; unlimited for view=full)")
	cmd.Flags().IntVar(&maxEdges, "max-edges", 0, "Max edges (0 uses view default; unlimited for view=full)")
	return cmd
}

type diagramFormat int

const (
	diagramFormatGraphviz diagramFormat = iota
	diagramFormatMermaid
	diagramFormatBoth
)

func parseDiagramFormat(in string) (diagramFormat, error) {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "", "graphviz", "dot":
		return diagramFormatGraphviz, nil
	case "mermaid", "mmd":
		return diagramFormatMermaid, nil
	case "both":
		return diagramFormatBoth, nil
	default:
		return diagramFormatGraphviz, fmt.Errorf("unsupported --format %q (supported: graphviz|mermaid|both)", in)
	}
}

func diagramSVGName(base string, mode diagramFormat, engine string) string {
	if mode == diagramFormatBoth {
		return fmt.Sprintf("%s.%s.svg", base, engine)
	}
	return base + ".svg"
}

func renderGraphviz(dotPath, svgPath, layout string) string {
	bin, err := exec.LookPath("dot")
	if err != nil {
		return "graphviz renderer not found (dot missing); wrote source only"
	}
	args := []string{"-Tsvg"}
	if strings.EqualFold(strings.TrimSpace(layout), string(diagram.LayoutSFDP)) {
		args = append(args, "-Ksfdp")
	}
	args = append(args, dotPath, "-o", svgPath)
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "graphviz render failed; wrote source only: " + msg
	}
	return ""
}

func renderMermaid(mmdPath, svgPath string) string {
	bin, err := exec.LookPath("mmdc")
	if err != nil {
		return "mermaid renderer not found (mmdc missing); wrote source only"
	}
	cmd := exec.Command(bin, "-i", mmdPath, "-o", svgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "mermaid render failed; wrote source only: " + msg
	}
	return ""
}

func resolveDiagramAccount(ctx context.Context, st *store.Store, profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile != "" {
		meta, ok, err := st.LookupProfile(ctx, profile)
		if err != nil {
			return "", err
		}
		if !ok || strings.TrimSpace(meta.AccountID) == "" {
			return "", fmt.Errorf("unknown profile %q in DB (run `awscope scan --profile %s ...` first)", profile, profile)
		}
		return meta.AccountID, nil
	}

	accts, err := st.ListAccounts(ctx)
	if err != nil {
		return "", err
	}
	if len(accts) == 0 {
		return "", fmt.Errorf("no accounts found in DB (run `awscope scan ...` first)")
	}
	if len(accts) == 1 {
		return accts[0].AccountID, nil
	}
	ids := make([]string, 0, len(accts))
	for _, a := range accts {
		ids = append(ids, a.AccountID)
	}
	sort.Strings(ids)
	return "", fmt.Errorf("multiple accounts found in DB (%s); pass --profile to scope diagram", strings.Join(ids, ","))
}

func dedupeEdges(edges []graph.RelationshipEdge) []graph.RelationshipEdge {
	if len(edges) <= 1 {
		return edges
	}
	out := make([]graph.RelationshipEdge, 0, len(edges))
	seen := map[string]struct{}{}
	for _, e := range edges {
		k := string(e.From) + "|" + e.Kind + "|" + string(e.To)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}

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

func sanitizeFilename(s string) string {
	if strings.TrimSpace(s) == "" {
		return "all"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "._-")
}

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Cache maintenance commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCacheStatsCmd())
	return cmd
}

func newActionCmd(dbPath *string, offline *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action",
		Short: "Run operational actions with audit logging",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newActionListCmd())
	cmd.AddCommand(newActionRunCmd(dbPath, offline))
	return cmd
}

func newActionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available action IDs",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range actionsRegistry.ListIDs() {
				fmt.Println(id)
			}
			return nil
		},
	}
}

func newActionRunCmd(dbPath *string, offline *bool) *cobra.Command {
	var (
		actionID string
		key      string
		profile  string
		confirm  bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an action against a specific resource_key",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			if offline != nil && *offline {
				return fmt.Errorf("--offline is not compatible with action run")
			}
			if actionID == "" {
				return fmt.Errorf("--id is required")
			}
			if key == "" {
				return fmt.Errorf("--key is required (resource_key from DB/export)")
			}
			if !confirm {
				return fmt.Errorf("refusing to run without --confirm")
			}

			a, ok := actionsRegistry.Get(actionID)
			if !ok {
				return fmt.Errorf("unknown action %q (known: %v)", actionID, actionsRegistry.ListIDs())
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			profileName := effectiveProfile(profile)

			// Validate action exists (keeps error message close to CLI input).
			_ = a

			res, err := core.RunAction(runCtx, st, actionID, graph.ResourceKey(key), profileName)
			if err != nil {
				return err
			}
			fmt.Printf("action complete: id=%s run_id=%s status=%s\n", res.ActionID, res.ActionRunID, res.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&actionID, "id", "", "Action ID (e.g. ec2.stop)")
	cmd.Flags().StringVar(&key, "key", "", "Resource key string")
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (defaults to AWS_PROFILE or 'default')")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Acknowledge this may change AWS resources")
	return cmd
}

func effectiveProfile(in string) string {
	if in != "" {
		return in
	}
	if env := os.Getenv("AWS_PROFILE"); env != "" {
		return env
	}
	return "default"
}

func newCacheStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Print cache/database stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			dbPath := mustRootFlag(cmd, "db-path")
			st, err := store.Open(store.OpenOptions{Path: dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			rCount, err := st.CountResources(runCtx)
			if err != nil {
				return err
			}
			eCount, err := st.CountEdges(runCtx)
			if err != nil {
				return err
			}
			fmt.Printf("db: %s\nresources: %d\nedges: %d\n", st.DBPath(), rCount, eCount)

			rows, err := st.CountResourcesByType(runCtx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Printf("- %s (%s): %d\n", r.Type, r.Service, r.Count)
			}
			return nil
		},
	}
}

func mustRootFlag(cmd *cobra.Command, name string) string {
	if cmd == nil || cmd.Root() == nil {
		return ""
	}
	f := cmd.Root().PersistentFlags().Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}
