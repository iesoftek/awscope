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

func (noneSet) Service(string) string                     { return "" }
func (noneSet) Type(string) string                        { return "" }
func (noneSet) Status(string) string                      { return "" }
func (noneSet) Relationship(kind, dir string) string       { return "" }

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
	default:
		return ""
	}
}

func (asciiSet) Type(typ string) string {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch t {
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
	default:
		return ""
	}
}

func (n nerdSet) Type(typ string) string {
	parts := strings.SplitN(strings.TrimSpace(typ), ":", 2)
	if len(parts) > 0 {
		return n.Service(parts[0])
	}
	return ""
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
