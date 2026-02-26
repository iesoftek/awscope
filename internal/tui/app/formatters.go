package app

import (
	"awscope/internal/catalog"
	"awscope/internal/cost"
	"awscope/internal/store"
	"awscope/internal/tui/icons"
	"awscope/internal/tui/theme"
	"awscope/internal/tui/widgets/table"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func cloneColumnSpecs(in []catalog.ColumnSpec) []catalog.ColumnSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]catalog.ColumnSpec, len(in))
	copy(out, in)
	return out
}

func resourcePresetColumns(preset catalog.TablePreset, includeCost bool) []catalog.ColumnSpec {
	specs := cloneColumnSpecs(preset.Columns)
	if len(specs) == 0 {
		specs = cloneColumnSpecs(catalog.ResourceTablePreset("", "").Columns)
	}
	if !includeCost {
		return specs
	}
	for _, s := range specs {
		if s.Kind == catalog.ColumnKindCost {
			return specs
		}
	}
	costCol := catalog.ColumnSpec{ID: "est_monthly", Title: "Est/mo", Kind: catalog.ColumnKindCost, Format: catalog.ValueFormatFloat, Width: 10, MinWidth: 8}
	insertAt := -1
	for i, s := range specs {
		if s.Kind == catalog.ColumnKindCreated {
			insertAt = i
			break
		}
	}
	if insertAt < 0 {
		return append(specs, costCol)
	}
	out := make([]catalog.ColumnSpec, 0, len(specs)+1)
	out = append(out, specs[:insertAt]...)
	out = append(out, costCol)
	out = append(out, specs[insertAt:]...)
	return out
}

func defaultColumnWidth(spec catalog.ColumnSpec) int {
	if spec.Width > 0 {
		return spec.Width
	}
	switch spec.Kind {
	case catalog.ColumnKindName:
		return 24
	case catalog.ColumnKindType:
		return 18
	case catalog.ColumnKindRegion:
		return 11
	case catalog.ColumnKindStatus:
		return 12
	case catalog.ColumnKindCreated:
		return 16
	case catalog.ColumnKindID:
		return 22
	case catalog.ColumnKindCost:
		return 10
	default:
		return 12
	}
}

func defaultColumnMinWidth(spec catalog.ColumnSpec) int {
	if spec.MinWidth > 0 {
		return spec.MinWidth
	}
	switch spec.Kind {
	case catalog.ColumnKindName:
		return 12
	case catalog.ColumnKindType:
		return 10
	case catalog.ColumnKindRegion:
		return 8
	case catalog.ColumnKindStatus:
		return 8
	case catalog.ColumnKindCreated:
		return 10
	case catalog.ColumnKindID:
		return 12
	case catalog.ColumnKindCost:
		return 8
	default:
		return 6
	}
}

func buildResourceColumns(width int, preset catalog.TablePreset, includeCost bool) []table.Column {
	if width <= 0 {
		width = 80
	}
	specs := resourcePresetColumns(preset, includeCost)
	if len(specs) == 0 {
		return nil
	}

	curW := make([]int, len(specs))
	minW := make([]int, len(specs))
	total := 0
	flexIdx := -1

	for i, spec := range specs {
		w := defaultColumnWidth(spec)
		mn := defaultColumnMinWidth(spec)
		if mn > w {
			mn = w
		}
		curW[i] = w
		minW[i] = mn
		total += w
		if flexIdx < 0 && spec.Kind == catalog.ColumnKindName {
			flexIdx = i
		}
	}

	if total > width {
		overflow := total - width
		order := make([]int, 0, len(curW))
		for i := len(curW) - 1; i >= 0; i-- {
			if specs[i].Kind == catalog.ColumnKindName {
				continue
			}
			order = append(order, i)
		}
		if flexIdx >= 0 {
			order = append(order, flexIdx)
		} else {
			for i := len(curW) - 1; i >= 0; i-- {
				order = append(order, i)
			}
		}

		for overflow > 0 {
			progressed := false
			for _, idx := range order {
				if overflow <= 0 {
					break
				}
				if curW[idx] > minW[idx] {
					curW[idx]--
					overflow--
					progressed = true
				}
			}
			if !progressed {
				break
			}
		}
	} else if total < width {
		extra := width - total
		idx := flexIdx
		if idx < 0 && len(curW) > 0 {
			idx = 0
		}
		if idx >= 0 {
			curW[idx] += extra
		}
	}

	cols := make([]table.Column, 0, len(specs))
	for i, spec := range specs {
		if curW[i] <= 0 {
			continue
		}
		cols = append(cols, table.Column{Title: spec.Title, Width: curW[i]})
	}
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

func makeResourceRows(ss []store.ResourceSummary, preset catalog.TablePreset, includeCost bool, styles theme.Styles, ic icons.Set) []table.Row {
	specs := resourcePresetColumns(preset, includeCost)
	rows := make([]table.Row, 0, len(ss))
	for _, s := range ss {
		row := make(table.Row, 0, len(specs))
		for _, spec := range specs {
			row = append(row, renderResourceCell(s, spec, styles, ic))
		}
		rows = append(rows, row)
	}
	return rows
}

func renderResourceCell(s store.ResourceSummary, spec catalog.ColumnSpec, styles theme.Styles, ic icons.Set) string {
	switch spec.Kind {
	case catalog.ColumnKindName:
		return renderNameForTable(s)
	case catalog.ColumnKindType:
		return styles.Dim.Render(s.Type)
	case catalog.ColumnKindRegion:
		return styles.Dim.Render(s.Region)
	case catalog.ColumnKindStatus:
		statusRaw := ""
		if strings.TrimSpace(spec.AttrKey) != "" {
			if v, _, ok := attrValueWithFallback(s.Attributes, spec.AttrKey); ok {
				statusRaw = strings.TrimSpace(valueToText(v))
			}
		} else {
			statusRaw = statusFromAttrs(s.Attributes)
		}
		return renderStatusForTable(statusRaw, styles, ic)
	case catalog.ColumnKindCreated:
		return styles.Dim.Render(renderCreatedValue(s.Attributes))
	case catalog.ColumnKindID:
		return renderIDForTable(s.PrimaryID)
	case catalog.ColumnKindCost:
		if s.EstMonthlyUSD == nil {
			return styles.Dim.Render("-")
		}
		return cost.FormatUSDPerMonthTable(*s.EstMonthlyUSD)
	case catalog.ColumnKindAttr:
		return renderAttrForTable(s.Attributes, spec, styles, ic)
	default:
		return styles.Dim.Render("-")
	}
}

func renderNameForTable(s store.ResourceSummary) string {
	name := strings.TrimSpace(s.DisplayName)
	if name == "" || name == s.PrimaryID {
		if tn := strings.TrimSpace(s.Tags["Name"]); tn != "" {
			name = tn
		}
	}
	if name == "" {
		name = s.PrimaryID
	}
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	return name
}

func renderIDForTable(primaryID string) string {
	id := strings.TrimSpace(primaryID)
	if id == "" {
		return "-"
	}
	if len(id) > 22 {
		return id[len(id)-22:]
	}
	return id
}

func renderCreatedValue(attrs map[string]any) string {
	if attrs == nil {
		return "-"
	}
	if v, _, ok := attrValueWithFallback(attrs, "created_at|createdAt|creationTime|lastModified|updated_at"); ok {
		if out := strings.TrimSpace(renderDateTimeRaw(v)); out != "" && out != "-" {
			return out
		}
	}
	return "-"
}

func renderAttrForTable(attrs map[string]any, spec catalog.ColumnSpec, styles theme.Styles, ic icons.Set) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	v, key, ok := attrValueWithFallback(attrs, spec.AttrKey)
	if !ok {
		return styles.Dim.Render("-")
	}

	switch spec.Format {
	case catalog.ValueFormatStatus:
		return renderStatusForTable(strings.TrimSpace(valueToText(v)), styles, ic)
	case catalog.ValueFormatBool:
		b, ok := parseBoolAny(v)
		if !ok {
			return styles.Dim.Render("-")
		}
		if b {
			return "true"
		}
		return "false"
	case catalog.ValueFormatInt:
		n, ok := parseInt64Any(v)
		if !ok {
			return styles.Dim.Render("-")
		}
		return fmt.Sprintf("%d", n)
	case catalog.ValueFormatFloat:
		f, ok := parseFloat64Any(v)
		if !ok {
			return styles.Dim.Render("-")
		}
		return formatFloat(f)
	case catalog.ValueFormatAgeDays:
		return renderAgeDaysValue(v, styles)
	case catalog.ValueFormatBytesGiB:
		keyLower := strings.ToLower(strings.TrimSpace(key))
		asBytes := strings.Contains(keyLower, "bytes")
		asGiB := strings.Contains(keyLower, "gib")
		return renderBytesGiBValue(v, asBytes, asGiB, styles)
	case catalog.ValueFormatDateTime:
		out := renderDateTimeRaw(v)
		if strings.TrimSpace(out) == "" || out == "-" {
			return styles.Dim.Render("-")
		}
		return out
	case catalog.ValueFormatListCount:
		n, ok := listCountAny(v)
		if !ok {
			return styles.Dim.Render("-")
		}
		return fmt.Sprintf("%d", n)
	case catalog.ValueFormatText:
		fallthrough
	default:
		text := strings.TrimSpace(valueToText(v))
		if text == "" || text == "<nil>" {
			return styles.Dim.Render("-")
		}
		return text
	}
}

func renderAgeDaysValue(v any, styles theme.Styles) string {
	n, ok := parseInt64Any(v)
	if !ok || n < 0 {
		return styles.Dim.Render("-")
	}
	return fmt.Sprintf("%dd", n)
}

func renderBytesGiBValue(v any, interpretAsBytes bool, interpretAsGiB bool, styles theme.Styles) string {
	f, ok := parseFloat64Any(v)
	if !ok {
		return styles.Dim.Render("-")
	}
	if interpretAsBytes {
		if f < 0 {
			return styles.Dim.Render("-")
		}
		gib := f / float64(1024*1024*1024)
		if gib <= 0 {
			return styles.Dim.Render("0GiB")
		}
		return fmt.Sprintf("%.2fGiB", gib)
	}
	if interpretAsGiB {
		if f < 0 {
			return styles.Dim.Render("-")
		}
		if f <= 0 {
			return styles.Dim.Render("0GiB")
		}
		return fmt.Sprintf("%.2fGiB", f)
	}

	// Fallback heuristic when key naming does not indicate units.
	if f > float64(1024*1024*10) {
		gib := f / float64(1024*1024*1024)
		if gib <= 0 {
			return styles.Dim.Render("0GiB")
		}
		return fmt.Sprintf("%.2fGiB", gib)
	}
	if f <= 0 {
		return styles.Dim.Render("0GiB")
	}
	return fmt.Sprintf("%.2fGiB", f)
}

func renderDateTimeRaw(v any) string {
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return "-"
		}
		return x
	case time.Time:
		if x.IsZero() {
			return "-"
		}
		return x.UTC().Format("2006-01-02 15:04")
	case *time.Time:
		if x == nil || x.IsZero() {
			return "-"
		}
		return x.UTC().Format("2006-01-02 15:04")
	default:
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" || s == "<nil>" {
			return "-"
		}
		return s
	}
}

func attrValueWithFallback(attrs map[string]any, keySpec string) (value any, key string, ok bool) {
	if attrs == nil {
		return nil, "", false
	}
	keys := strings.Split(strings.TrimSpace(keySpec), "|")
	var first any
	var firstKey string
	foundFirst := false
	for _, rawKey := range keys {
		k := strings.TrimSpace(rawKey)
		if k == "" {
			continue
		}
		v, exists := attrs[k]
		if !exists {
			continue
		}
		if !foundFirst {
			first = v
			firstKey = k
			foundFirst = true
		}
		if valueIsNonEmpty(v) {
			return v, k, true
		}
	}
	if foundFirst {
		return first, firstKey, true
	}
	return nil, "", false
}

func valueIsNonEmpty(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		return x != "" && x != "-"
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i != 0
		}
		if f, err := x.Float64(); err == nil {
			return f != 0
		}
		return false
	case int:
		return x != 0
	case int8:
		return x != 0
	case int16:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case uint:
		return x != 0
	case uint8:
		return x != 0
	case uint16:
		return x != 0
	case uint32:
		return x != 0
	case uint64:
		return x != 0
	case float32:
		return x != 0
	case float64:
		return x != 0
	case bool:
		// false is still meaningful for booleans.
		return true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return valueIsNonEmpty(rv.Elem().Interface())
	default:
		return true
	}
}

func valueToText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case []string:
		return strings.Join(x, ",")
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			parts = append(parts, strings.TrimSpace(fmt.Sprint(it)))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func parseInt64Any(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint:
		return int64(x), true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint64:
		if x > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(x), true
	case float32:
		return int64(x), true
	case float64:
		return int64(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func parseFloat64Any(v any) (float64, bool) {
	switch x := v.(type) {
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func parseBoolAny(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "true", "1", "yes", "y", "enabled", "on":
			return true, true
		case "false", "0", "no", "n", "disabled", "off":
			return false, true
		default:
			return false, false
		}
	default:
		n, ok := parseInt64Any(v)
		if !ok {
			return false, false
		}
		return n != 0, true
	}
}

func listCountAny(v any) (int, bool) {
	switch x := v.(type) {
	case []string:
		return len(x), true
	case []any:
		return len(x), true
	case map[string]any:
		return len(x), true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0, false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len(), true
	default:
		n, ok := parseInt64Any(v)
		if !ok {
			return 0, false
		}
		return int(n), true
	}
}

func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	out := strconv.FormatFloat(v, 'f', 2, 64)
	out = strings.TrimRight(strings.TrimRight(out, "0"), ".")
	if out == "" || out == "-0" {
		return "0"
	}
	return out
}

func renderAgeDaysForTable(attrs map[string]any, styles theme.Styles) string {
	v, _, ok := attrValueWithFallback(attrs, "age_days")
	if !ok {
		return styles.Dim.Render("-")
	}
	return renderAgeDaysValue(v, styles)
}

func renderLastUsedForTable(attrs map[string]any, styles theme.Styles) string {
	v, _, ok := attrValueWithFallback(attrs, "last_used_at")
	if !ok {
		return styles.Dim.Render("-")
	}
	text := strings.TrimSpace(valueToText(v))
	if text == "" || text == "-" {
		return styles.Dim.Render("-")
	}
	return text
}

func renderStoredGiBForTable(attrs map[string]any, styles theme.Styles) string {
	v, key, ok := attrValueWithFallback(attrs, "storedGiB|storedBytes")
	if !ok {
		return styles.Dim.Render("-")
	}
	keyLower := strings.ToLower(strings.TrimSpace(key))
	asBytes := strings.Contains(keyLower, "bytes")
	asGiB := strings.Contains(keyLower, "gib")
	return renderBytesGiBValue(v, asBytes, asGiB, styles)
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
