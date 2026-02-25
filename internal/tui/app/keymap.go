package app

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Quit        key.Binding
	Focus       key.Binding
	Filter      key.Binding
	Regions     key.Binding
	Actions     key.Binding
	Refresh     key.Binding
	Theme       key.Binding
	Graph       key.Binding
	Pricing     key.Binding
	Audit       key.Binding
	AuditDetail key.Binding
	AuditJump   key.Binding
	AuditFacets key.Binding
	AuditWindow key.Binding
	AuditErrors key.Binding
	AuditPage   key.Binding
	PrevPage    key.Binding
	NextPage    key.Binding
	Back        key.Binding
	Help        key.Binding

	PaneNav       key.Binding
	PaneResources key.Binding
	PaneDetails   key.Binding

	Summary key.Binding
	Related key.Binding
	Raw     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Focus, k.Filter, k.Regions, k.Actions, k.Graph, k.Audit, k.Pricing, k.PrevPage, k.NextPage, k.Back, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Focus, k.Filter, k.Regions, k.Actions, k.Graph, k.Audit, k.Refresh},
		{k.PaneNav, k.PaneResources, k.PaneDetails, k.Summary, k.Related, k.Raw, k.Pricing, k.Theme},
		{k.AuditDetail, k.AuditJump, k.AuditFacets, k.AuditWindow, k.AuditErrors, k.AuditPage},
		{k.PrevPage, k.NextPage, k.Back},
		{k.Help, k.Quit},
	}
}
