package app

import "strings"

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func computePaneWidths(w int) (left, mid, right int) {
	const (
		minLeft  = 18
		minMid   = 30
		minRight = 30
	)
	if w <= 0 {
		return minLeft, minMid, minRight
	}

	left = max(minLeft, w/6)
	left = min(left, 32)

	mid = max(minMid, (w*50)/100)
	right = w - left - mid

	if right < minRight {
		need := minRight - right

		shrinkMid := min(need, max(0, mid-minMid))
		mid -= shrinkMid
		need -= shrinkMid

		if need > 0 {
			shrinkLeft := min(need, max(0, left-minLeft))
			left -= shrinkLeft
			need -= shrinkLeft
		}

		right = w - left - mid
	}

	if right < 0 {
		right = 0
	}
	return left, mid, right
}
