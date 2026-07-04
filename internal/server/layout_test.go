package server

import "testing"

func TestLayoutStoreNormalizesAndPersistsState(t *testing.T) {
	t.Parallel()

	store := NewLayoutStore()
	state := store.Set(LayoutState{
		ActiveTabID: "tab-2",
		Theme:       "Catppuccin Mocha",
		Version:     7,
		Tabs: []TerminalTabLayout{
			{
				ID:          "tab-1",
				Title:       "first",
				CustomTitle: true,
				Layout: LayoutNode{
					Type:      "split",
					Direction: "vertical",
					Children: []LayoutNode{
						{Type: "pane", ID: "pane-1"},
						{Type: "pane", ID: "pane-2"},
					},
				},
				ActivePaneID: "pane-2",
			},
			{
				ID:           "tab-2",
				Title:        "second",
				Layout:       LayoutNode{Type: "pane", ID: "pane-3"},
				ActivePaneID: "missing",
			},
		},
	})

	if state.ActiveTabID != "tab-2" {
		t.Fatalf("ActiveTabID = %q", state.ActiveTabID)
	}
	if len(state.Tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(state.Tabs))
	}
	if !state.Initialized {
		t.Fatal("state should be marked initialized after Set")
	}
	if state.Version != 7 {
		t.Fatalf("version = %d, want 7", state.Version)
	}
	if state.Theme != "Catppuccin Mocha" {
		t.Fatalf("theme = %q", state.Theme)
	}
	if got := state.Tabs[0].Panes; len(got) != 2 || got[0] != "pane-1" || got[1] != "pane-2" {
		t.Fatalf("first panes = %#v", got)
	}
	if got := state.Tabs[0].Layout.Sizes; len(got) != 2 || got[0] != 50 || got[1] != 50 {
		t.Fatalf("first split sizes = %#v, want [50 50]", got)
	}
	if state.Tabs[1].ActivePaneID != "pane-3" {
		t.Fatalf("second active pane = %q", state.Tabs[1].ActivePaneID)
	}

	state.Tabs[0].Title = "mutated"
	state.Tabs[0].Layout.Sizes[0] = 90
	stored := store.Get()
	if stored.Tabs[0].Title != "first" {
		t.Fatalf("store returned mutable state: %q", stored.Tabs[0].Title)
	}
	if stored.Tabs[0].Layout.Sizes[0] != 50 {
		t.Fatalf("store returned mutable split sizes: %#v", stored.Tabs[0].Layout.Sizes)
	}
}

func TestLayoutStoreIgnoresStaleWrites(t *testing.T) {
	t.Parallel()

	store := NewLayoutStore()
	newer := store.Set(LayoutState{
		ActiveTabID: "tab-new",
		Version:     5,
		Tabs: []TerminalTabLayout{{
			ID:           "tab-new",
			Title:        "new",
			Layout:       LayoutNode{Type: "pane", ID: "pane-new"},
			ActivePaneID: "pane-new",
		}},
	})
	stale := store.Set(LayoutState{
		ActiveTabID: "tab-old",
		Version:     4,
		Tabs: []TerminalTabLayout{{
			ID:           "tab-old",
			Title:        "old",
			Layout:       LayoutNode{Type: "pane", ID: "pane-old"},
			ActivePaneID: "pane-old",
		}},
	})

	if stale.ActiveTabID != newer.ActiveTabID || stale.Version != newer.Version {
		t.Fatalf("stale write result = %#v, want current %#v", stale, newer)
	}
	stored := store.Get()
	if stored.ActiveTabID != "tab-new" || stored.Version != 5 {
		t.Fatalf("stored layout = %#v, want newer layout", stored)
	}
}

func TestLayoutStorePersistsEmptyInitializedState(t *testing.T) {
	t.Parallel()

	store := NewLayoutStore()
	state := store.Set(LayoutState{Tabs: []TerminalTabLayout{}, ActiveTabID: ""})
	if !state.Initialized {
		t.Fatal("empty saved layout should be marked initialized")
	}
	if len(state.Tabs) != 0 {
		t.Fatalf("tabs = %d, want 0", len(state.Tabs))
	}
	if got := store.Get(); !got.Initialized || len(got.Tabs) != 0 {
		t.Fatalf("stored empty layout = %#v, want initialized empty state", got)
	}
}

func TestLayoutStateRejectsDuplicatePanes(t *testing.T) {
	t.Parallel()

	state := normalizeLayoutState(LayoutState{
		Tabs: []TerminalTabLayout{
			{ID: "tab-1", Title: "one", Layout: LayoutNode{Type: "pane", ID: "pane-1"}},
			{ID: "tab-2", Title: "two", Layout: LayoutNode{Type: "pane", ID: "pane-1"}},
		},
	})

	if len(state.Tabs) != 1 {
		t.Fatalf("tabs = %d, want duplicate pane tab dropped", len(state.Tabs))
	}
}

func TestLayoutStatePreservesValidSplitSizes(t *testing.T) {
	t.Parallel()

	state := normalizeLayoutState(LayoutState{
		Tabs: []TerminalTabLayout{{
			ID:    "tab-1",
			Title: "one",
			Layout: LayoutNode{
				Type:      "split",
				Direction: "vertical",
				Sizes:     []int{60, 40},
				Children: []LayoutNode{
					{Type: "pane", ID: "pane-1"},
					{Type: "pane", ID: "pane-2"},
				},
			},
		}},
	})

	got := state.Tabs[0].Layout.Sizes
	if len(got) != 2 || got[0] != 60 || got[1] != 40 {
		t.Fatalf("sizes = %#v, want [60 40]", got)
	}
}

func TestLayoutStateNormalizesInvalidSplitSizes(t *testing.T) {
	t.Parallel()

	state := normalizeLayoutState(LayoutState{
		Tabs: []TerminalTabLayout{{
			ID:    "tab-1",
			Title: "one",
			Layout: LayoutNode{
				Type:      "split",
				Direction: "vertical",
				Sizes:     []int{95, 5},
				Children: []LayoutNode{
					{Type: "pane", ID: "pane-1"},
					{Type: "pane", ID: "pane-2"},
				},
			},
		}},
	})

	got := state.Tabs[0].Layout.Sizes
	if len(got) != 2 || got[0] != 50 || got[1] != 50 {
		t.Fatalf("sizes = %#v, want [50 50]", got)
	}
}

func TestLayoutStateScalesSplitSizesAfterDroppingInvalidChild(t *testing.T) {
	t.Parallel()

	state := normalizeLayoutState(LayoutState{
		Tabs: []TerminalTabLayout{{
			ID:    "tab-1",
			Title: "one",
			Layout: LayoutNode{
				Type:      "split",
				Direction: "vertical",
				Sizes:     []int{60, 30, 10},
				Children: []LayoutNode{
					{Type: "pane", ID: "pane-1"},
					{Type: "pane", ID: ""},
					{Type: "pane", ID: "pane-2"},
				},
			},
		}},
	})

	got := state.Tabs[0].Layout.Sizes
	if len(got) != 2 || got[0] != 86 || got[1] != 14 {
		t.Fatalf("sizes = %#v, want [86 14]", got)
	}
}
