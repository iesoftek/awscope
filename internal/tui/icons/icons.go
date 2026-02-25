package icons

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

type Mode string

const (
	ModeASCII Mode = "ascii"
	ModeNerd  Mode = "nerd"
	ModeNone  Mode = "none"
)

type Set interface {
	Service(service string) string
	Type(typ string) string
	Status(status string) string
	Relationship(kind, dir string) string
}

func ParseMode(s string) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeNerd:
		return ModeNerd
	case ModeNone:
		return ModeNone
	case ModeASCII:
		return ModeASCII
	default:
		return ModeNerd
	}
}

func New(mode Mode) Set {
	switch mode {
	case ModeNone:
		return noneSet{}
	case ModeNerd:
		return nerdSet{}
	default:
		return asciiSet{}
	}
}

// Pad truncates/pads a string to exactly w display columns.
// If s is empty, it returns w spaces.
func Pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return strings.Repeat(" ", w)
	}
	if runewidth.StringWidth(s) > w {
		return runewidth.Truncate(s, w, "")
	}
	if runewidth.StringWidth(s) < w {
		return s + strings.Repeat(" ", w-runewidth.StringWidth(s))
	}
	return s
}

type noneSet struct{}

func (noneSet) Service(string) string                { return "" }
func (noneSet) Type(string) string                   { return "" }
func (noneSet) Status(string) string                 { return "" }
func (noneSet) Relationship(kind, dir string) string { return "" }

type asciiSet struct{}

func (asciiSet) Service(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "ec2":
		return "E"
	case "ecs":
		return "C"
	case "elbv2":
		return "B"
	case "iam":
		return "I"
	case "kms":
		return "K"
	case "lambda":
		return "L"
	case "rds":
		return "R"
	case "s3":
		return "S"
	case "logs":
		return "G"
	case "secretsmanager":
		return "Z"
	case "sns":
		return "N"
	case "sqs":
		return "Q"
	case "dynamodb":
		return "D"
	case "autoscaling":
		return "A"
	case "sagemaker":
		return "M"
	case "identitycenter":
		return "C"
	case "cloudtrail":
		return "T"
	case "config":
		return "F"
	case "guardduty":
		return "G"
	case "securityhub":
		return "H"
	case "accessanalyzer":
		return "X"
	case "wafv2":
		return "W"
	case "acm":
		return "M"
	case "cloudfront":
		return "F"
	case "apigateway":
		return "P"
	case "ecr":
		return "R"
	case "eks":
		return "K"
	case "elasticache":
		return "C"
	case "opensearch":
		return "O"
	case "redshift":
		return "R"
	case "msk":
		return "M"
	case "efs":
		return "E"
	default:
		return ""
	}
}

func (asciiSet) Type(typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch t {
	case "iam:user":
		return "u"
	case "iam:group":
		return "G"
	case "iam:access-key":
		return "k"
	case "ec2:instance":
		return "i"
	case "ec2:security-group":
		return "g"
	case "ec2:subnet":
		return "s"
	case "ec2:vpc":
		return "v"
	case "ec2:volume":
		return "d"
	case "ec2:snapshot":
		return "p"
	case "ec2:ami":
		return "a"
	case "ec2:internet-gateway":
		return "i"
	case "ec2:placement-group":
		return "p"
	case "ec2:launch-template":
		return "t"
	case "ec2:key-pair":
		return "k"
	case "ec2:nat-gateway":
		return "n"
	case "ec2:eip":
		return "e"
	case "ec2:route-table":
		return "r"
	case "ec2:nacl":
		return "n"
	case "ec2:network-interface":
		return "i"
	case "ecs:service":
		return "s"
	case "ecs:cluster":
		return "c"
	case "ecs:task-definition":
		return "t"
	case "elbv2:load-balancer":
		return "l"
	case "elbv2:target-group":
		return "g"
	case "rds:db-instance":
		return "i"
	case "rds:db-cluster":
		return "c"
	case "logs:log-group":
		return "g"
	case "autoscaling:group":
		return "a"
	case "autoscaling:launch-configuration":
		return "l"
	case "autoscaling:instance":
		return "i"
	case "sagemaker:endpoint":
		return "e"
	case "sagemaker:endpoint-config":
		return "c"
	case "sagemaker:model":
		return "m"
	case "sagemaker:notebook-instance":
		return "n"
	case "sagemaker:training-job":
		return "t"
	case "sagemaker:processing-job":
		return "p"
	case "sagemaker:transform-job":
		return "x"
	case "sagemaker:domain":
		return "d"
	case "sagemaker:user-profile":
		return "u"
	case "identitycenter:instance":
		return "i"
	case "identitycenter:permission-set":
		return "p"
	case "identitycenter:assignment":
		return "a"
	case "identitycenter:user":
		return "u"
	case "identitycenter:group":
		return "g"
	case "cloudtrail:trail":
		return "t"
	case "config:recorder":
		return "r"
	case "config:delivery-channel":
		return "d"
	case "guardduty:detector":
		return "d"
	case "securityhub:hub":
		return "h"
	case "securityhub:standard-subscription":
		return "s"
	case "accessanalyzer:analyzer":
		return "a"
	case "wafv2:web-acl":
		return "w"
	case "acm:certificate":
		return "c"
	case "cloudfront:distribution":
		return "d"
	case "apigateway:rest-api":
		return "a"
	case "apigateway:domain-name":
		return "d"
	case "ecr:repository":
		return "r"
	case "eks:cluster":
		return "k"
	case "eks:nodegroup":
		return "n"
	case "elasticache:replication-group":
		return "r"
	case "elasticache:cache-cluster":
		return "c"
	case "elasticache:subnet-group":
		return "s"
	case "opensearch:domain":
		return "o"
	case "redshift:cluster":
		return "r"
	case "redshift:subnet-group":
		return "s"
	case "msk:cluster":
		return "m"
	case "efs:file-system":
		return "f"
	case "efs:mount-target":
		return "t"
	case "efs:access-point":
		return "a"
	default:
		return ""
	}
}

func (asciiSet) Status(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "running", "available", "active", "inservice", "in-service", "ok", "enabled":
		return "+"
	case "stopped", "inactive", "disabled":
		return "-"
	case "pending", "creating", "modifying", "updating", "provisioning", "starting", "stopping", "deleting":
		return "~"
	case "failed", "error", "unhealthy":
		return "!"
	default:
		return ""
	}
}

func (asciiSet) Relationship(kind, dir string) string {
	_ = kind
	if strings.ToLower(strings.TrimSpace(dir)) == "in" {
		return "<"
	}
	if strings.ToLower(strings.TrimSpace(dir)) == "out" {
		return ">"
	}
	return ""
}

// nerdSet is intentionally small; users must have a Nerd Font in their terminal.
type nerdSet struct{}

func (nerdSet) Service(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "ec2":
		return "󰌗" // server
	case "ecs":
		return "󰘚" // containers
	case "elbv2":
		return "󰒍" // load balancer-ish
	case "iam":
		return "󰌾" // user
	case "kms":
		return "󰌋" // key
	case "lambda":
		return "󰘧" // lambda
	case "rds":
		return "󰆼" // database
	case "s3":
		return "󰉉" // bucket
	case "logs":
		return "󰌥" // file-document
	case "secretsmanager":
		return "󰌆" // lock
	case "sns":
		return "󰎟" // bell
	case "sqs":
		return "󰅧" // queue-ish
	case "dynamodb":
		return "󰆧" // table-ish
	case "autoscaling":
		return "󰹹"
	case "sagemaker":
		return "󰬛"
	case "identitycenter":
		return "󰍂"
	case "cloudtrail":
		return "󰙅"
	case "config":
		return "󰒓"
	case "guardduty":
		return "󰒃"
	case "securityhub":
		return "󰦝"
	case "accessanalyzer":
		return "󰈸"
	case "wafv2":
		return "󰒍"
	case "acm":
		return "󰄴"
	case "cloudfront":
		return "󰕮"
	case "apigateway":
		return "󰴋"
	case "ecr":
		return "󰡨"
	case "eks":
		return "󱃾"
	case "elasticache":
		return "󰆼"
	case "opensearch":
		return "󰍉"
	case "redshift":
		return "󰚰"
	case "msk":
		return "󰜎"
	case "efs":
		return "󰈔"
	default:
		return ""
	}
}

func (n nerdSet) Type(typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch t {
	case "iam:user":
		return "󰌾"
	case "iam:group":
		return "󰉖"
	case "iam:access-key":
		return "󰌋"
	default:
		parts := strings.SplitN(strings.TrimSpace(typ), ":", 2)
		if len(parts) > 0 {
			return n.Service(parts[0])
		}
		return ""
	}
}

func (nerdSet) Status(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "running", "available", "active", "inservice", "in-service", "ok", "enabled":
		return "󰄬" // check
	case "stopped", "inactive", "disabled":
		return "󰅖" // stop
	case "pending", "creating", "modifying", "updating", "provisioning", "starting", "stopping", "deleting":
		return "󰑓" // spinner-ish
	case "failed", "error", "unhealthy":
		return "󰅚" // x
	default:
		return ""
	}
}

func (nerdSet) Relationship(kind, dir string) string {
	_ = kind
	if strings.ToLower(strings.TrimSpace(dir)) == "in" {
		return "󰁍"
	}
	if strings.ToLower(strings.TrimSpace(dir)) == "out" {
		return "󰁔"
	}
	return ""
}
