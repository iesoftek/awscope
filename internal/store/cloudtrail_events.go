package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"awscope/internal/graph"
)

type CloudTrailEventRow struct {
	EventID string

	AccountID string
	Partition string
	Region    string

	EventTime   time.Time
	EventSource string
	EventName   string
	Action      string // create | delete
	Service     string

	ResourceKey  graph.ResourceKey
	ResourceType string
	ResourceName string
	ResourceArn  string

	Username     string
	PrincipalArn string
	SourceIP     string
	UserAgent    string
	ReadOnly     string
	ErrorCode    string
	ErrorMessage string

	EventJSON []byte
	IndexedAt time.Time
}

type CloudTrailEventSummary struct {
	EventID string

	AccountID string
	Partition string
	Region    string

	EventTime   time.Time
	EventSource string
	EventName   string
	Action      string
	Service     string

	ResourceKey  graph.ResourceKey
	ResourceType string
	ResourceName string
	ResourceArn  string

	Username     string
	PrincipalArn string
	SourceIP     string
	ErrorCode    string
}

type CloudTrailEventQuery struct {
	Regions    []string
	Text       string
	Actions    []string
	Services   []string
	EventNames []string
	Since      *time.Time
	Until      *time.Time
	OnlyErrors bool
	Limit      int
}

type CloudTrailCursor struct {
	EventTime string // RFC3339Nano
	EventID   string
}

type CloudTrailCursorPage struct {
	Events     []CloudTrailEventSummary
	NextCursor *CloudTrailCursor
	PrevCursor *CloudTrailCursor
	HasNext    bool
	HasPrev    bool
}

type FacetCount struct {
	Value string
	Count int
}

type CloudTrailFacets struct {
	Actions    []FacetCount
	Services   []FacetCount
	EventNames []FacetCount
}

func (s *Store) UpsertCloudTrailEvents(ctx context.Context, rows []CloudTrailEventRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO cloudtrail_events(
  event_id, account_id, partition, region, event_time,
  event_source, event_name, action, service,
  resource_key, resource_type, resource_name, resource_arn,
  username, principal_arn, source_ip, user_agent, read_only, error_code, error_message,
  event_json, indexed_at
) VALUES (
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?, ?, ?, ?, ?, ?,
  ?, ?
)
ON CONFLICT(event_id) DO UPDATE SET
  account_id=excluded.account_id,
  partition=excluded.partition,
  region=excluded.region,
  event_time=excluded.event_time,
  event_source=excluded.event_source,
  event_name=excluded.event_name,
  action=excluded.action,
  service=excluded.service,
  resource_key=excluded.resource_key,
  resource_type=excluded.resource_type,
  resource_name=excluded.resource_name,
  resource_arn=excluded.resource_arn,
  username=excluded.username,
  principal_arn=excluded.principal_arn,
  source_ip=excluded.source_ip,
  user_agent=excluded.user_agent,
  read_only=excluded.read_only,
  error_code=excluded.error_code,
  error_message=excluded.error_message,
  event_json=excluded.event_json,
  indexed_at=excluded.indexed_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, r := range rows {
		eventID := strings.TrimSpace(r.EventID)
		if eventID == "" {
			continue
		}
		indexedAt := r.IndexedAt
		if indexedAt.IsZero() {
			indexedAt = now
		}
		evJSON := strings.TrimSpace(string(r.EventJSON))
		if evJSON == "" {
			evJSON = "{}"
		}
		if _, err := stmt.ExecContext(
			ctx,
			eventID,
			strings.TrimSpace(r.AccountID),
			strings.TrimSpace(r.Partition),
			strings.TrimSpace(r.Region),
			r.EventTime.UTC().Format(time.RFC3339Nano),
			strings.TrimSpace(r.EventSource),
			strings.TrimSpace(r.EventName),
			strings.TrimSpace(r.Action),
			strings.TrimSpace(r.Service),
			nullIfEmpty(string(r.ResourceKey)),
			nullIfEmpty(strings.TrimSpace(r.ResourceType)),
			nullIfEmpty(strings.TrimSpace(r.ResourceName)),
			nullIfEmpty(strings.TrimSpace(r.ResourceArn)),
			nullIfEmpty(strings.TrimSpace(r.Username)),
			nullIfEmpty(strings.TrimSpace(r.PrincipalArn)),
			nullIfEmpty(strings.TrimSpace(r.SourceIP)),
			nullIfEmpty(strings.TrimSpace(r.UserAgent)),
			nullIfEmpty(strings.TrimSpace(r.ReadOnly)),
			nullIfEmpty(strings.TrimSpace(r.ErrorCode)),
			nullIfEmpty(strings.TrimSpace(r.ErrorMessage)),
			evJSON,
			indexedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) PruneCloudTrailEventsOlderThan(ctx context.Context, accountID string, cutoff time.Time) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
DELETE FROM cloudtrail_events
WHERE account_id = ? AND event_time < ?
`, accountID, cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) CountCloudTrailEvents(ctx context.Context, accountID string, regions []string, filter string) (int, error) {
	return s.CountCloudTrailEventsByQuery(ctx, accountID, CloudTrailEventQuery{
		Regions: regions,
		Text:    filter,
	})
}

func (s *Store) ListCloudTrailEventsPaged(ctx context.Context, accountID string, regions []string, filter string, limit, offset int) ([]CloudTrailEventSummary, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	scope, args := buildCloudTrailWhere(CloudTrailEventQuery{
		Regions: regions,
		Text:    filter,
	}, accountID)

	args = append(args, limit, offset)
	q := fmt.Sprintf(`
SELECT
  event_id, account_id, partition, region, event_time,
  event_source, event_name, action, service,
  resource_key, resource_type, resource_name, resource_arn,
  username, principal_arn, source_ip, error_code
FROM cloudtrail_events
%s
ORDER BY event_time DESC, event_id DESC
LIMIT ? OFFSET ?
`, scope)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CloudTrailEventSummary
	for rows.Next() {
		var (
			r CloudTrailEventSummary

			eventTime    string
			resourceKey  sql.NullString
			resourceType sql.NullString
			resourceName sql.NullString
			resourceArn  sql.NullString
			username     sql.NullString
			principalArn sql.NullString
			sourceIP     sql.NullString
			errorCode    sql.NullString
		)
		if err := rows.Scan(
			&r.EventID,
			&r.AccountID,
			&r.Partition,
			&r.Region,
			&eventTime,
			&r.EventSource,
			&r.EventName,
			&r.Action,
			&r.Service,
			&resourceKey,
			&resourceType,
			&resourceName,
			&resourceArn,
			&username,
			&principalArn,
			&sourceIP,
			&errorCode,
		); err != nil {
			return nil, err
		}

		r.EventTime, _ = time.Parse(time.RFC3339Nano, eventTime)
		r.ResourceKey = graph.ResourceKey(resourceKey.String)
		r.ResourceType = resourceType.String
		r.ResourceName = resourceName.String
		r.ResourceArn = resourceArn.String
		r.Username = username.String
		r.PrincipalArn = principalArn.String
		r.SourceIP = sourceIP.String
		r.ErrorCode = errorCode.String

		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildCloudTrailWhere(q CloudTrailEventQuery, accountID string) (string, []any) {
	accountID = strings.TrimSpace(accountID)
	scope := "WHERE account_id = ?"
	args := []any{accountID}

	if rs := sanitizeCloudTrailVals(q.Regions); len(rs) > 0 {
		holders := make([]string, 0, len(rs))
		for _, r := range rs {
			holders = append(holders, "?")
			args = append(args, r)
		}
		scope += fmt.Sprintf(" AND region IN (%s)", strings.Join(holders, ","))
	}
	if acts := sanitizeCloudTrailVals(q.Actions); len(acts) > 0 {
		holders := make([]string, 0, len(acts))
		for _, a := range acts {
			holders = append(holders, "?")
			args = append(args, a)
		}
		scope += fmt.Sprintf(" AND action IN (%s)", strings.Join(holders, ","))
	}
	if svcs := sanitizeCloudTrailVals(q.Services); len(svcs) > 0 {
		holders := make([]string, 0, len(svcs))
		for _, s := range svcs {
			holders = append(holders, "?")
			args = append(args, s)
		}
		scope += fmt.Sprintf(" AND service IN (%s)", strings.Join(holders, ","))
	}
	if evs := sanitizeCloudTrailVals(q.EventNames); len(evs) > 0 {
		holders := make([]string, 0, len(evs))
		for _, e := range evs {
			holders = append(holders, "?")
			args = append(args, e)
		}
		scope += fmt.Sprintf(" AND event_name IN (%s)", strings.Join(holders, ","))
	}
	if q.Since != nil {
		scope += " AND event_time >= ?"
		args = append(args, q.Since.UTC().Format(time.RFC3339Nano))
	}
	if q.Until != nil {
		scope += " AND event_time <= ?"
		args = append(args, q.Until.UTC().Format(time.RFC3339Nano))
	}
	if q.OnlyErrors {
		scope += " AND error_code IS NOT NULL AND TRIM(error_code) <> ''"
	}

	text := strings.TrimSpace(q.Text)
	if text != "" {
		like := "%" + text + "%"
		scope += `
 AND (
  event_name LIKE ?
  OR event_source LIKE ?
  OR service LIKE ?
  OR action LIKE ?
  OR resource_type LIKE ?
  OR resource_name LIKE ?
  OR resource_arn LIKE ?
  OR username LIKE ?
  OR principal_arn LIKE ?
  OR event_id LIKE ?
 )`
		for i := 0; i < 10; i++ {
			args = append(args, like)
		}
	}
	return scope, args
}

func sanitizeCloudTrailVals(vs []string) []string {
	if len(vs) == 0 {
		return nil
	}
	uniq := make(map[string]struct{}, len(vs))
	for _, v := range vs {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		uniq[v] = struct{}{}
	}
	if len(uniq) == 0 {
		return nil
	}
	out := make([]string, 0, len(uniq))
	for v := range uniq {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func cursorFromSummary(s CloudTrailEventSummary) *CloudTrailCursor {
	return &CloudTrailCursor{
		EventTime: s.EventTime.UTC().Format(time.RFC3339Nano),
		EventID:   s.EventID,
	}
}

func reverseCloudTrailSummaries(rows []CloudTrailEventSummary) {
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
}

func (s *Store) CountCloudTrailEventsByQuery(ctx context.Context, accountID string, q CloudTrailEventQuery) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, nil
	}
	scope, args := buildCloudTrailWhere(q, accountID)
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`
SELECT COUNT(1)
FROM cloudtrail_events
%s
`, scope), args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ListCloudTrailEventsByCursor(ctx context.Context, accountID string, q CloudTrailEventQuery, after *CloudTrailCursor, before *CloudTrailCursor) (CloudTrailCursorPage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return CloudTrailCursorPage{}, nil
	}
	if after != nil && before != nil {
		return CloudTrailCursorPage{}, fmt.Errorf("after and before cursors are mutually exclusive")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}

	scope, args := buildCloudTrailWhere(q, accountID)
	order := "ORDER BY event_time DESC, event_id DESC"
	if after != nil {
		scope += " AND (event_time < ? OR (event_time = ? AND event_id < ?))"
		args = append(args, strings.TrimSpace(after.EventTime), strings.TrimSpace(after.EventTime), strings.TrimSpace(after.EventID))
	}
	if before != nil {
		scope += " AND (event_time > ? OR (event_time = ? AND event_id > ?))"
		args = append(args, strings.TrimSpace(before.EventTime), strings.TrimSpace(before.EventTime), strings.TrimSpace(before.EventID))
		order = "ORDER BY event_time ASC, event_id ASC"
	}

	args = append(args, limit+1)
	query := fmt.Sprintf(`
SELECT
  event_id, account_id, partition, region, event_time,
  event_source, event_name, action, service,
  resource_key, resource_type, resource_name, resource_arn,
  username, principal_arn, source_ip, error_code
FROM cloudtrail_events
%s
%s
LIMIT ?
`, scope, order)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return CloudTrailCursorPage{}, err
	}
	defer rows.Close()

	out := make([]CloudTrailEventSummary, 0, limit+1)
	for rows.Next() {
		var (
			r CloudTrailEventSummary

			eventTime    string
			resourceKey  sql.NullString
			resourceType sql.NullString
			resourceName sql.NullString
			resourceArn  sql.NullString
			username     sql.NullString
			principalArn sql.NullString
			sourceIP     sql.NullString
			errorCode    sql.NullString
		)
		if err := rows.Scan(
			&r.EventID,
			&r.AccountID,
			&r.Partition,
			&r.Region,
			&eventTime,
			&r.EventSource,
			&r.EventName,
			&r.Action,
			&r.Service,
			&resourceKey,
			&resourceType,
			&resourceName,
			&resourceArn,
			&username,
			&principalArn,
			&sourceIP,
			&errorCode,
		); err != nil {
			return CloudTrailCursorPage{}, err
		}
		r.EventTime, _ = time.Parse(time.RFC3339Nano, eventTime)
		r.ResourceKey = graph.ResourceKey(resourceKey.String)
		r.ResourceType = resourceType.String
		r.ResourceName = resourceName.String
		r.ResourceArn = resourceArn.String
		r.Username = username.String
		r.PrincipalArn = principalArn.String
		r.SourceIP = sourceIP.String
		r.ErrorCode = errorCode.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return CloudTrailCursorPage{}, err
	}

	page := CloudTrailCursorPage{}
	hadExtra := len(out) > limit
	if hadExtra {
		out = out[:limit]
	}
	if before != nil {
		reverseCloudTrailSummaries(out)
	}
	page.Events = out
	if len(out) > 0 {
		page.PrevCursor = cursorFromSummary(out[0])
		page.NextCursor = cursorFromSummary(out[len(out)-1])
	}
	switch {
	case before != nil:
		page.HasPrev = hadExtra
		page.HasNext = true
	case after != nil:
		page.HasPrev = true
		page.HasNext = hadExtra
	default:
		page.HasPrev = false
		page.HasNext = hadExtra
	}
	return page, nil
}

func (s *Store) ListCloudTrailEventFacets(ctx context.Context, accountID string, q CloudTrailEventQuery, limit int) (CloudTrailFacets, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return CloudTrailFacets{}, nil
	}
	if limit <= 0 {
		limit = 200
	}

	loadFacet := func(scope string, args []any, col string, limit int) ([]FacetCount, error) {
		query := fmt.Sprintf(`
SELECT %s, COUNT(1)
FROM cloudtrail_events
%s
GROUP BY %s
ORDER BY COUNT(1) DESC, %s ASC
LIMIT ?
`, col, scope, col, col)
		fargs := append(append([]any{}, args...), limit)
		rows, err := s.db.QueryContext(ctx, query, fargs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []FacetCount
		for rows.Next() {
			var v sql.NullString
			var c int
			if err := rows.Scan(&v, &c); err != nil {
				return nil, err
			}
			val := strings.TrimSpace(v.String)
			if val == "" {
				continue
			}
			out = append(out, FacetCount{Value: val, Count: c})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}

	scope, args := buildCloudTrailWhere(q, accountID)
	actions, err := loadFacet(scope, args, "action", 10)
	if err != nil {
		return CloudTrailFacets{}, err
	}
	services, err := loadFacet(scope, args, "service", 64)
	if err != nil {
		return CloudTrailFacets{}, err
	}
	// Event-name facet should not constrain itself by currently-selected event names.
	q2 := q
	q2.EventNames = nil
	scope2, args2 := buildCloudTrailWhere(q2, accountID)
	eventNames, err := loadFacet(scope2, args2, "event_name", limit)
	if err != nil {
		return CloudTrailFacets{}, err
	}
	return CloudTrailFacets{
		Actions:    actions,
		Services:   services,
		EventNames: eventNames,
	}, nil
}

func (s *Store) GetCloudTrailEventByID(ctx context.Context, accountID, eventID string) (CloudTrailEventRow, bool, error) {
	accountID = strings.TrimSpace(accountID)
	eventID = strings.TrimSpace(eventID)
	if accountID == "" || eventID == "" {
		return CloudTrailEventRow{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
  event_id, account_id, partition, region, event_time,
  event_source, event_name, action, service,
  resource_key, resource_type, resource_name, resource_arn,
  username, principal_arn, source_ip, user_agent, read_only, error_code, error_message,
  event_json, indexed_at
FROM cloudtrail_events
WHERE account_id = ? AND event_id = ?
LIMIT 1
`, accountID, eventID)

	var (
		out CloudTrailEventRow

		eventTime    string
		resourceKey  sql.NullString
		resourceType sql.NullString
		resourceName sql.NullString
		resourceArn  sql.NullString
		username     sql.NullString
		principalArn sql.NullString
		sourceIP     sql.NullString
		userAgent    sql.NullString
		readOnly     sql.NullString
		errorCode    sql.NullString
		errorMessage sql.NullString
		eventJSON    string
		indexedAt    string
	)

	err := row.Scan(
		&out.EventID,
		&out.AccountID,
		&out.Partition,
		&out.Region,
		&eventTime,
		&out.EventSource,
		&out.EventName,
		&out.Action,
		&out.Service,
		&resourceKey,
		&resourceType,
		&resourceName,
		&resourceArn,
		&username,
		&principalArn,
		&sourceIP,
		&userAgent,
		&readOnly,
		&errorCode,
		&errorMessage,
		&eventJSON,
		&indexedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return CloudTrailEventRow{}, false, nil
		}
		return CloudTrailEventRow{}, false, err
	}

	out.EventTime, _ = time.Parse(time.RFC3339Nano, eventTime)
	out.ResourceKey = graph.ResourceKey(resourceKey.String)
	out.ResourceType = resourceType.String
	out.ResourceName = resourceName.String
	out.ResourceArn = resourceArn.String
	out.Username = username.String
	out.PrincipalArn = principalArn.String
	out.SourceIP = sourceIP.String
	out.UserAgent = userAgent.String
	out.ReadOnly = readOnly.String
	out.ErrorCode = errorCode.String
	out.ErrorMessage = errorMessage.String
	out.EventJSON = []byte(eventJSON)
	out.IndexedAt, _ = time.Parse(time.RFC3339Nano, indexedAt)
	return out, true, nil
}
