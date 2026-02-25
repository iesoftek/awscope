package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"awscope/internal/diagram"
	"awscope/internal/graph"
	"awscope/internal/store"

	"github.com/spf13/cobra"
)

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
