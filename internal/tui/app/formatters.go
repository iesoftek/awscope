package app

import (
	"awscope/internal/cost"
	"awscope/internal/store"
	"awscope/internal/tui/icons"
	"awscope/internal/tui/theme"
	"awscope/internal/tui/widgets/table"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func buildResourceColumns(width int, includeCost bool, includeStored bool, includeIAMKeyFields bool) []table.Column {
	// Reserve widths for non-name fields; name gets the remainder.
	if width <= 0 {
		width = 80
	}
	regionW := 10
	typeW := 18
	statusW := 12 // includes optional status icon prefix
	storedW := 10
	ageW := 6
	lastUsedW := 16
	idW := 22
	createdW := 19
	costW := 10
	sep := 6 // padding-ish
	fixed := regionW + typeW + statusW + idW + createdW + sep
	if includeStored {
		fixed += storedW
	}
	if includeIAMKeyFields {
		fixed += ageW + lastUsedW
	}
	if includeCost {
		fixed += costW
	}
	nameW := width - fixed
	if nameW < 12 {
		nameW = 12
	}
	cols := []table.Column{
		{Title: "Name", Width: nameW},
		{Title: "Type", Width: typeW},
		{Title: "Region", Width: regionW},
		{Title: "Status", Width: statusW},
	}
	if includeIAMKeyFields {
		cols = append(cols,
			table.Column{Title: "Age", Width: ageW},
			table.Column{Title: "Last Used", Width: lastUsedW},
		)
	}
	if includeStored {
		cols = append(cols, table.Column{Title: "Stored", Width: storedW})
	}
	if includeCost {
		cols = append(cols, table.Column{Title: "Est/mo", Width: costW})
	}
	cols = append(cols,
		table.Column{Title: "Created", Width: createdW},
		table.Column{Title: "ID", Width: idW},
	)
	return cols
}

func buildAuditColumns(width int) []table.Column {
	if width <= 0 {
		width = 100
	}
	timeW := 19
	ageW := 7
	actionW := 7
	serviceW := 12
	eventW := 20
	regionW := 11
	actorW := 14
	sep := 8
	fixed := timeW + ageW + actionW + serviceW + eventW + regionW + actorW + sep
	resourceW := width - fixed
	if resourceW < 16 {
		resourceW = 16
	}
	return []table.Column{
		{Title: "Time", Width: timeW},
		{Title: "Age", Width: ageW},
		{Title: "Action", Width: actionW},
		{Title: "Service", Width: serviceW},
		{Title: "Event", Width: eventW},
		{Title: "Resource", Width: resourceW},
		{Title: "Region", Width: regionW},
		{Title: "Actor", Width: actorW},
	}
}

func ageLabel(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func makeAuditRows(events []store.CloudTrailEventSummary, styles theme.Styles, ic icons.Set) []table.Row {
	rows := make([]table.Row, 0, len(events))
	for _, ev := range events {
		when := ev.EventTime.UTC().Format("2006-01-02 15:04:05")
		action := strings.ToUpper(strings.TrimSpace(ev.Action))
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(ev.Action), 2)
		}
		switch strings.ToLower(strings.TrimSpace(ev.Action)) {
		case "create":
			action = styles.Good.Render(icon + action)
		case "delete":
			action = styles.Bad.Render(icon + action)
		default:
			action = styles.Dim.Render(icon + action)
		}

		resource := firstNonEmpty(strings.TrimSpace(ev.ResourceName), strings.TrimSpace(ev.ResourceArn), strings.TrimSpace(ev.ResourceType), "-")
		actor := firstNonEmpty(strings.TrimSpace(ev.Username), strings.TrimSpace(ev.PrincipalArn), "-")
		rows = append(rows, table.Row{
			styles.Dim.Render(when),
			styles.Dim.Render(ageLabel(ev.EventTime)),
			action,
			ev.Service,
			ev.EventName,
			resource,
			styles.Dim.Render(ev.Region),
			actor,
		})
	}
	return rows
}

func makeResourceRows(ss []store.ResourceSummary, includeCost bool, styles theme.Styles, ic icons.Set, includeStored bool, includeIAMKeyFields bool) []table.Row {
	rows := make([]table.Row, 0, len(ss))
	for _, s := range ss {
		name := strings.TrimSpace(s.DisplayName)
		if name == "" || name == s.PrimaryID {
			if tn := strings.TrimSpace(s.Tags["Name"]); tn != "" {
				name = tn
			}
		}
		if name == "" {
			name = s.PrimaryID
		}

		statusRaw := statusFromAttrs(s.Attributes)
		status := renderStatusForTable(statusRaw, styles, ic)
		age := ""
		lastUsed := ""
		if includeIAMKeyFields {
			age = renderAgeDaysForTable(s.Attributes, styles)
			lastUsed = renderLastUsedForTable(s.Attributes, styles)
		}
		stored := ""
		if includeStored {
			stored = renderStoredGiBForTable(s.Attributes, styles)
		}
		created := ""
		if v, ok := s.Attributes["created_at"].(string); ok {
			created = v
		} else {
			// Unknown or not available from the AWS API for this resource type.
			created = "-"
		}
		id := s.PrimaryID
		if len(id) > 22 {
			id = id[len(id)-22:]
		}
		costStr := ""
		if includeCost {
			if s.EstMonthlyUSD == nil {
				costStr = styles.Dim.Render("-")
			} else {
				costStr = cost.FormatUSDPerMonthTable(*s.EstMonthlyUSD)
			}
		}
		base := table.Row{
			name,
			styles.Dim.Render(s.Type),
			styles.Dim.Render(s.Region),
			status,
		}
		if includeIAMKeyFields {
			base = append(base, age, lastUsed)
		}
		if includeStored {
			base = append(base, stored)
		}
		if includeCost {
			base = append(base, costStr)
		}
		base = append(base,
			styles.Dim.Render(created),
			id,
		)
		rows = append(rows, base)
	}
	return rows
}

func renderAgeDaysForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	var n int64 = -1
	switch v := attrs["age_days"].(type) {
	case int:
		n = int64(v)
	case int64:
		n = v
	case float64:
		n = int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			n = i
		}
	case string:
		if i, err := json.Number(strings.TrimSpace(v)).Int64(); err == nil {
			n = i
		}
	}
	if n < 0 {
		return styles.Dim.Render("-")
	}
	return fmt.Sprintf("%dd", n)
}

func renderLastUsedForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	if v, ok := attrs["last_used_at"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" || v == "-" {
			return styles.Dim.Render("-")
		}
		return v
	}
	return styles.Dim.Render("-")
}

func renderStoredGiBForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	if v, ok := attrs["storedGiB"].(float64); ok {
		if v <= 0 {
			return styles.Dim.Render("0GiB")
		}
		return fmt.Sprintf("%.2fGiB", v)
	}

	var b float64
	switch x := attrs["storedBytes"].(type) {
	case int64:
		b = float64(x)
	case int:
		b = float64(x)
	case float64:
		b = x
	case float32:
		b = float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			b = f
		}
	case string:
		if n, err := json.Number(strings.TrimSpace(x)).Float64(); err == nil {
			b = n
		}
	}
	if b <= 0 {
		return styles.Dim.Render("-")
	}
	gib := b / float64(1024*1024*1024)
	if gib <= 0 {
		return styles.Dim.Render("0GiB")
	}
	return fmt.Sprintf("%.2fGiB", gib)
}

func statusFromAttrs(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	// Common keys across providers.
	for _, k := range []string{
		"state",      // ec2/elbv2/lambda
		"status",     // rds/ecs/dynamodb
		"keyState",   // kms
		"lastStatus", // ecs task
		"desiredStatus",
	} {
		if v, ok := attrs[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func renderStatusForTable(raw string, styles theme.Styles, ic icons.Set) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(""), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + "-")
	}
	s := strings.ToLower(raw)
	switch s {
	// Good-ish.
	case "running", "available", "active", "enabled", "attached", "inuse", "in-use", "ok", "healthy", "console":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Good.Render(icon + raw)
	// In progress / transitional.
	case "pending", "provisioning", "creating", "updating", "modifying", "starting", "stopping", "rebooting", "initializing", "backing-up":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Warn.Render(icon + raw)
	// Bad / failure.
	case "failed", "error", "deleted", "deleting", "terminated", "unhealthy":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Bad.Render(icon + raw)
	// Neutral/dim.
	case "stopped", "inactive", "disabled", "detached", "programmatic", "unknown":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + raw)
	default:
		// Preserve original string; use dim for unknown statuses to reduce noise.
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + raw)
	}
}
