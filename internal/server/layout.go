package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type LayoutNode struct {
	Type      string       `json:"type"`
	ID        string       `json:"id,omitempty"`
	Direction string       `json:"direction,omitempty"`
	Children  []LayoutNode `json:"children,omitempty"`
}

type TerminalTabLayout struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	CustomTitle  bool       `json:"customTitle"`
	Layout       LayoutNode `json:"layout"`
	Panes        []string   `json:"panes"`
	ActivePaneID string     `json:"activePaneId"`
}

type LayoutState struct {
	Tabs        []TerminalTabLayout `json:"tabs"`
	ActiveTabID string              `json:"activeTabId"`
	Theme       string              `json:"theme,omitempty"`
	Initialized bool                `json:"initialized"`
	Version     uint64              `json:"version"`
}

type LayoutStore struct {
	mu    sync.RWMutex
	state LayoutState
}

func NewLayoutStore() *LayoutStore {
	return &LayoutStore{}
}

func NewLayoutStoreWithDefaultTheme(themeName string) *LayoutStore {
	return &LayoutStore{state: LayoutState{Theme: strings.TrimSpace(themeName)}}
}

func (s *LayoutStore) Get() LayoutState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneLayoutState(s.state)
}

func (s *LayoutStore) Set(state LayoutState) LayoutState {
	normalized := normalizeLayoutState(state)
	normalized.Initialized = true
	s.mu.Lock()
	if s.state.Initialized && normalized.Version < s.state.Version {
		current := cloneLayoutState(s.state)
		s.mu.Unlock()
		return current
	}
	s.state = normalized
	s.mu.Unlock()

	return cloneLayoutState(normalized)
}

func (s *Server) handleGetLayout(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.layout.Get())
}

func (s *Server) handlePutLayout(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var state LayoutState
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		http.Error(w, "invalid layout", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.layout.Set(state))
}

func normalizeLayoutState(state LayoutState) LayoutState {
	normalized := LayoutState{Tabs: make([]TerminalTabLayout, 0, len(state.Tabs)), Theme: strings.TrimSpace(state.Theme), Initialized: state.Initialized, Version: state.Version}
	seenTabs := map[string]struct{}{}
	seenPanes := map[string]struct{}{}

	for _, tab := range state.Tabs {
		if tab.ID == "" {
			continue
		}
		if _, ok := seenTabs[tab.ID]; ok {
			continue
		}

		layout, ok := normalizeLayoutNode(tab.Layout, seenPanes)
		if !ok {
			continue
		}
		panes := collectLayoutPaneIDs(layout)
		if len(panes) == 0 {
			continue
		}
		activePaneID := tab.ActivePaneID
		if !containsString(panes, activePaneID) {
			activePaneID = panes[0]
		}
		title := tab.Title
		if title == "" {
			title = "#?"
		}

		seenTabs[tab.ID] = struct{}{}
		normalized.Tabs = append(normalized.Tabs, TerminalTabLayout{
			ID:           tab.ID,
			Title:        title,
			CustomTitle:  tab.CustomTitle,
			Layout:       layout,
			Panes:        panes,
			ActivePaneID: activePaneID,
		})
	}

	if containsTabID(normalized.Tabs, state.ActiveTabID) {
		normalized.ActiveTabID = state.ActiveTabID
	} else if len(normalized.Tabs) > 0 {
		normalized.ActiveTabID = normalized.Tabs[0].ID
	}

	return normalized
}

func normalizeLayoutNode(node LayoutNode, seenPanes map[string]struct{}) (LayoutNode, bool) {
	switch node.Type {
	case "pane":
		if node.ID == "" {
			return LayoutNode{}, false
		}
		if _, ok := seenPanes[node.ID]; ok {
			return LayoutNode{}, false
		}
		seenPanes[node.ID] = struct{}{}
		return LayoutNode{Type: "pane", ID: node.ID}, true
	case "split":
		if node.Direction != "vertical" && node.Direction != "horizontal" {
			return LayoutNode{}, false
		}
		children := make([]LayoutNode, 0, len(node.Children))
		for _, child := range node.Children {
			normalizedChild, ok := normalizeLayoutNode(child, seenPanes)
			if ok {
				children = append(children, normalizedChild)
			}
		}
		if len(children) == 0 {
			return LayoutNode{}, false
		}
		if len(children) == 1 {
			return children[0], true
		}
		return LayoutNode{Type: "split", Direction: node.Direction, Children: children}, true
	default:
		return LayoutNode{}, false
	}
}

func collectLayoutPaneIDs(node LayoutNode) []string {
	if node.Type == "pane" {
		return []string{node.ID}
	}

	ids := []string{}
	for _, child := range node.Children {
		ids = append(ids, collectLayoutPaneIDs(child)...)
	}
	return ids
}

func cloneLayoutState(state LayoutState) LayoutState {
	clone := LayoutState{
		Tabs:        make([]TerminalTabLayout, len(state.Tabs)),
		ActiveTabID: state.ActiveTabID,
		Theme:       state.Theme,
		Initialized: state.Initialized,
		Version:     state.Version,
	}
	for i, tab := range state.Tabs {
		clone.Tabs[i] = TerminalTabLayout{
			ID:           tab.ID,
			Title:        tab.Title,
			CustomTitle:  tab.CustomTitle,
			Layout:       cloneLayoutNode(tab.Layout),
			Panes:        append([]string(nil), tab.Panes...),
			ActivePaneID: tab.ActivePaneID,
		}
	}
	return clone
}

func cloneLayoutNode(node LayoutNode) LayoutNode {
	clone := node
	if node.Children != nil {
		clone.Children = make([]LayoutNode, len(node.Children))
		for i, child := range node.Children {
			clone.Children[i] = cloneLayoutNode(child)
		}
	}
	return clone
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsTabID(tabs []TerminalTabLayout, id string) bool {
	for _, tab := range tabs {
		if tab.ID == id {
			return true
		}
	}
	return false
}
