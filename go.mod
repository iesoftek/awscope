module awscope

go 1.25.0

require (
	github.com/aws/aws-sdk-go-v2 v1.41.2
	github.com/aws/aws-sdk-go-v2/config v1.32.7
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.63.1
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.55.0
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.288.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.71.0
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.54.6
	github.com/aws/aws-sdk-go-v2/service/iam v1.53.2
	github.com/aws/aws-sdk-go-v2/service/kms v1.49.5
	github.com/aws/aws-sdk-go-v2/service/lambda v1.88.0
	github.com/aws/aws-sdk-go-v2/service/pricing v1.40.11
	github.com/aws/aws-sdk-go-v2/service/rds v1.115.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.96.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.41.1
	github.com/aws/aws-sdk-go-v2/service/sns v1.39.11
	github.com/aws/aws-sdk-go-v2/service/sqs v1.42.21
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.6
	github.com/aws/smithy-go v1.24.1
	github.com/charmbracelet/bubbles v1.0.0
	github.com/charmbracelet/bubbletea v1.3.10
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/charmbracelet/log v0.4.2
	github.com/charmbracelet/x/ansi v0.11.6
	github.com/google/uuid v1.6.0
	github.com/mattn/go-runewidth v0.0.19
	github.com/mistakenelf/teacup v0.4.1
	github.com/pressly/goose/v3 v3.26.0
	github.com/spf13/cobra v1.10.2
	golang.org/x/sync v0.17.0
	modernc.org/sqlite v1.45.0
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.4 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.7 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.18 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.18 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/accessanalyzer v1.45.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/acm v1.37.20 // indirect
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.38.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.64.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.60.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.55.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/configservice v1.61.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecr v1.55.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/efs v1.41.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/eks v1.80.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.51.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/guardduty v1.73.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/identitystore v1.36.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/kafka v1.48.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.57.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/redshift v1.62.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.233.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/securityhub v1.67.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssoadmin v1.37.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.70.8 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.4.1 // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.9.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sahilm/fuzzy v0.1.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.27.0 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
