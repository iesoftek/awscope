package app

import "awscope/internal/catalog"

func defaultTypeForService(service string) string {
	return catalog.DefaultType(service)
}

func fallbackTypesForService(service string) []string {
	types := catalog.FallbackTypes(service)
	if len(types) > 0 {
		return types
	}
	if t := defaultTypeForService(service); t != "" {
		return []string{t}
	}
	return nil
}
