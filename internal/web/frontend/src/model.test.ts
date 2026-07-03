import { describe, expect, it } from 'vitest';
import { collectPaneIds, removePaneFromLayout, splitLayout, tabIndexFromShortcutKey } from './model';
import type { LayoutNode } from './types';

describe('splitLayout', () => {
  it('wraps the target pane in a split', () => {
    const layout: LayoutNode = { type: 'pane', id: 'pane-1' };

    expect(splitLayout(layout, 'pane-1', 'vertical', 'pane-2')).toEqual({
      type: 'split',
      direction: 'vertical',
      children: [
        { type: 'pane', id: 'pane-1' },
        { type: 'pane', id: 'pane-2' },
      ],
    });
  });

  it('only changes the matching nested pane', () => {
    const layout: LayoutNode = {
      type: 'split',
      direction: 'horizontal',
      children: [
        { type: 'pane', id: 'pane-1' },
        { type: 'pane', id: 'pane-2' },
      ],
    };

    expect(splitLayout(layout, 'pane-2', 'vertical', 'pane-3')).toEqual({
      type: 'split',
      direction: 'horizontal',
      children: [
        { type: 'pane', id: 'pane-1' },
        {
          type: 'split',
          direction: 'vertical',
          children: [
            { type: 'pane', id: 'pane-2' },
            { type: 'pane', id: 'pane-3' },
          ],
        },
      ],
    });
  });
});

describe('removePaneFromLayout', () => {
  it('removes a pane and collapses a split with one child', () => {
    const layout: LayoutNode = {
      type: 'split',
      direction: 'vertical',
      children: [
        { type: 'pane', id: 'pane-1' },
        { type: 'pane', id: 'pane-2' },
      ],
    };

    expect(removePaneFromLayout(layout, 'pane-1')).toEqual({ type: 'pane', id: 'pane-2' });
  });

  it('removes a nested pane without disturbing siblings', () => {
    const layout: LayoutNode = {
      type: 'split',
      direction: 'horizontal',
      children: [
        { type: 'pane', id: 'pane-1' },
        {
          type: 'split',
          direction: 'vertical',
          children: [
            { type: 'pane', id: 'pane-2' },
            { type: 'pane', id: 'pane-3' },
          ],
        },
      ],
    };

    expect(removePaneFromLayout(layout, 'pane-3')).toEqual({
      type: 'split',
      direction: 'horizontal',
      children: [
        { type: 'pane', id: 'pane-1' },
        { type: 'pane', id: 'pane-2' },
      ],
    });
  });

  it('returns null when the last pane is removed', () => {
    expect(removePaneFromLayout({ type: 'pane', id: 'pane-1' }, 'pane-1')).toBeNull();
  });
});

describe('collectPaneIds', () => {
  it('collects pane IDs from a nested layout in order', () => {
    const layout: LayoutNode = {
      type: 'split',
      direction: 'horizontal',
      children: [
        { type: 'pane', id: 'pane-1' },
        {
          type: 'split',
          direction: 'vertical',
          children: [
            { type: 'pane', id: 'pane-2' },
            { type: 'pane', id: 'pane-3' },
          ],
        },
      ],
    };

    expect(collectPaneIds(layout)).toEqual(['pane-1', 'pane-2', 'pane-3']);
  });
});

describe('tabIndexFromShortcutKey', () => {
  it('maps visible tab shortcut numbers to zero-based indexes', () => {
    expect(tabIndexFromShortcutKey('1')).toBe(0);
    expect(tabIndexFromShortcutKey('9')).toBe(8);
  });

  it('ignores unsupported shortcut keys', () => {
    expect(tabIndexFromShortcutKey('0')).toBeNull();
    expect(tabIndexFromShortcutKey('d')).toBeNull();
    expect(tabIndexFromShortcutKey('10')).toBeNull();
  });
});
