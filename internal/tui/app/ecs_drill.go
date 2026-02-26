package app

import (
	"fmt"
	"strings"

	"awscope/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) clearECSDrill() {
	if m == nil {
		return
	}
	m.ecsDrill = ecsDrillState{}
}

func (m *model) clearECSDrillIfRegionOutOfScope() {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.ecsDrill.Region) == "" || m.ecsDrill.Level == ecsDrillNone {
		return
	}
	region := strings.TrimSpace(m.ecsDrill.Region)
	for _, r := range m.selectedRegionSlice() {
		if strings.EqualFold(strings.TrimSpace(r), region) {
			return
		}
	}
	m.clearECSDrill()
}

func (m *model) applyECSSelection(service, typ string, fromNavigatorType bool) {
	if m == nil {
		return
	}
	service = strings.TrimSpace(service)
	typ = strings.TrimSpace(typ)

	if service != "ecs" {
		m.clearECSDrill()
		return
	}
	switch typ {
	case "ecs:cluster":
		if m.ecsDrill.Level != ecsDrillNone {
			m.clearECSDrill()
		}
	case "ecs:service":
		if fromNavigatorType && m.ecsDrill.Level != ecsDrillServices {
			m.clearECSDrill()
		}
	case "ecs:task":
		if fromNavigatorType && m.ecsDrill.Level != ecsDrillTasks {
			m.clearECSDrill()
		}
	default:
		m.clearECSDrill()
	}
}

func (m model) ecsDrillBreadcrumb() string {
	if strings.TrimSpace(m.selectedService) != "ecs" {
		return ""
	}
	switch m.ecsDrill.Level {
	case ecsDrillServices:
		cluster := strings.TrimSpace(m.ecsDrill.ClusterName)
		if cluster == "" {
			cluster = "cluster"
		}
		return fmt.Sprintf("ECS: %s / services", cluster)
	case ecsDrillTasks:
		cluster := strings.TrimSpace(m.ecsDrill.ClusterName)
		if cluster == "" {
			cluster = "cluster"
		}
		service := strings.TrimSpace(m.ecsDrill.ServiceName)
		if service == "" {
			service = "service"
		}
		return fmt.Sprintf("ECS: %s / %s / tasks", cluster, service)
	default:
		return "ECS: clusters"
	}
}

func ecsServiceNameFromSummary(s store.ResourceSummary) string {
	if v, ok := s.Attributes["serviceName"]; ok {
		if x := strings.TrimSpace(fmt.Sprintf("%v", v)); x != "" {
			return x
		}
	}
	name := strings.TrimSpace(s.DisplayName)
	if name == "" {
		name = strings.TrimSpace(s.PrimaryID)
	}
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

func (m model) ecsDrillScopeForQuery() (store.ECSDrillScope, bool) {
	if strings.TrimSpace(m.selectedService) != "ecs" {
		return store.ECSDrillScope{}, false
	}
	switch strings.TrimSpace(m.selectedType) {
	case "ecs:service":
		if m.ecsDrill.Level != ecsDrillServices || m.ecsDrill.ClusterKey == "" {
			return store.ECSDrillScope{}, false
		}
		return store.ECSDrillScope{
			Level:      "services",
			ClusterKey: m.ecsDrill.ClusterKey,
			ClusterArn: m.ecsDrill.ClusterArn,
			Region:     m.ecsDrill.Region,
		}, true
	case "ecs:task":
		if m.ecsDrill.Level != ecsDrillTasks {
			return store.ECSDrillScope{}, false
		}
		if m.ecsDrill.ServiceKey == "" && strings.TrimSpace(m.ecsDrill.ServiceName) == "" {
			return store.ECSDrillScope{}, false
		}
		return store.ECSDrillScope{
			Level:       "tasks",
			ClusterKey:  m.ecsDrill.ClusterKey,
			ClusterArn:  m.ecsDrill.ClusterArn,
			ServiceKey:  m.ecsDrill.ServiceKey,
			ServiceName: m.ecsDrill.ServiceName,
			Region:      m.ecsDrill.Region,
		}, true
	default:
		return store.ECSDrillScope{}, false
	}
}

func (m *model) drillECSDownCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(m.selectedService) != "ecs" || m.graphMode {
		return nil
	}
	s, ok := m.selectedSummary()
	if !ok {
		m.statusLine = "no ECS resource selected"
		return nil
	}

	switch strings.TrimSpace(m.selectedType) {
	case "ecs:cluster":
		m.ecsDrill = ecsDrillState{
			Level:       ecsDrillServices,
			ClusterKey:  s.Key,
			ClusterArn:  strings.TrimSpace(s.Arn),
			ClusterName: strings.TrimSpace(s.DisplayName),
			Region:      strings.TrimSpace(s.Region),
		}
		m.selectedType = "ecs:service"
		m.nav.SetSelection(m.selectedService, m.selectedType)
		m.pager.Page = 0
		m.pendingSelectKey = ""
		m.loading = true
		m.err = nil
		m.statusLine = fmt.Sprintf("ecs drill: %s -> services", firstNonEmpty(s.DisplayName, s.PrimaryID))
		return m.loadResourcesCmd()

	case "ecs:service":
		clusterArn := m.ecsDrill.ClusterArn
		if v, ok := s.Attributes["clusterArn"]; ok {
			if x := strings.TrimSpace(fmt.Sprintf("%v", v)); x != "" {
				clusterArn = x
			}
		}
		m.ecsDrill = ecsDrillState{
			Level:       ecsDrillTasks,
			ClusterKey:  m.ecsDrill.ClusterKey,
			ClusterArn:  clusterArn,
			ClusterName: m.ecsDrill.ClusterName,
			ServiceKey:  s.Key,
			ServiceArn:  strings.TrimSpace(s.Arn),
			ServiceName: ecsServiceNameFromSummary(s),
			Region:      strings.TrimSpace(s.Region),
		}
		m.selectedType = "ecs:task"
		m.nav.SetSelection(m.selectedService, m.selectedType)
		m.pager.Page = 0
		m.pendingSelectKey = ""
		m.loading = true
		m.err = nil
		m.statusLine = fmt.Sprintf("ecs drill: %s -> tasks", firstNonEmpty(s.DisplayName, s.PrimaryID))
		return m.loadResourcesCmd()

	case "ecs:task":
		m.statusLine = "ecs drill: already at task level"
		return nil
	default:
		return nil
	}
}

func (m *model) drillECSUpCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(m.selectedService) != "ecs" || m.graphMode {
		return nil
	}

	switch m.ecsDrill.Level {
	case ecsDrillTasks:
		serviceKey := m.ecsDrill.ServiceKey
		m.ecsDrill.Level = ecsDrillServices
		m.ecsDrill.ServiceKey = ""
		m.ecsDrill.ServiceArn = ""
		m.ecsDrill.ServiceName = ""
		m.selectedType = "ecs:service"
		m.nav.SetSelection(m.selectedService, m.selectedType)
		m.pager.Page = 0
		m.pendingSelectKey = serviceKey
		m.loading = true
		m.err = nil
		m.statusLine = "ecs drill: back to services"
		return m.loadResourcesCmd()

	case ecsDrillServices:
		clusterKey := m.ecsDrill.ClusterKey
		m.clearECSDrill()
		m.selectedType = "ecs:cluster"
		m.nav.SetSelection(m.selectedService, m.selectedType)
		m.pager.Page = 0
		m.pendingSelectKey = clusterKey
		m.loading = true
		m.err = nil
		m.statusLine = "ecs drill: back to clusters"
		return m.loadResourcesCmd()
	default:
		return nil
	}
}
