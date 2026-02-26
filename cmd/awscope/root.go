package main

import (
	"context"

	_ "awscope/internal/actions/ec2"
	_ "awscope/internal/actions/ecs"
	_ "awscope/internal/actions/sns"
	_ "awscope/internal/actions/sqs"

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

func newRootCommand(ctx context.Context, dbPath *string, offline *bool) *cobra.Command {
	root := &cobra.Command{
		Use:           "awscope",
		Short:         "AWS resource browser TUI + inventory",
		SilenceErrors: true, // main() prints the error once for consistent formatting.
	}
	root.PersistentFlags().StringVar(dbPath, "db-path", "", "Path to SQLite database (default: platform-specific user data dir)")
	root.PersistentFlags().BoolVar(offline, "offline", false, "Disable AWS calls; browse cached inventory only")

	root.AddCommand(newTuiCmd(ctx, dbPath, offline))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newScanCmd(dbPath, offline))
	root.AddCommand(newSecurityCmd(dbPath))
	root.AddCommand(newDiagramCmd(dbPath))
	root.AddCommand(newExportCmd(dbPath))
	root.AddCommand(newCacheCmd())
	root.AddCommand(newActionCmd(dbPath, offline))

	// Default to TUI if no subcommand is provided.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return newTuiCmd(ctx, dbPath, offline).RunE(cmd, args)
	}
	return root
}
