import { describe, expect, it } from 'vitest';
import {
  collectPaneIds,
  findAdjacentPaneId,
  paneDirectionFromShortcutKey,
  removePaneFromLayout,
  splitLayout,
  tabIndexFromShortcutKey,
} from './model';
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

describe('paneDirectionFromShortcutKey', () => {
  it('maps arrow shortcut keys to pane navigation directions', () => {
    expect(paneDirectionFromShortcutKey('ArrowLeft')).toBe('left');
    expect(paneDirectionFromShortcutKey('arrowright')).toBe('right');
    expect(paneDirectionFromShortcutKey('ArrowUp')).toBe('up');
    expect(paneDirectionFromShortcutKey('ArrowDown')).toBe('down');
  });

  it('ignores non-arrow shortcut keys', () => {
    expect(paneDirectionFromShortcutKey('d')).toBeNull();
    expect(paneDirectionFromShortcutKey('Left')).toBeNull();
  });
});

describe('findAdjacentPaneId', () => {
  const layout: LayoutNode = {
    type: 'split',
    direction: 'vertical',
    children: [
      {
        type: 'split',
        direction: 'horizontal',
        children: [
          { type: 'pane', id: 'pane-left-top' },
          { type: 'pane', id: 'pane-left-bottom' },
        ],
      },
      { type: 'pane', id: 'pane-right' },
    ],
  };

  it('finds panes across vertical and horizontal split boundaries', () => {
    expect(findAdjacentPaneId(layout, 'pane-left-top', 'down')).toBe('pane-left-bottom');
    expect(findAdjacentPaneId(layout, 'pane-left-bottom', 'up')).toBe('pane-left-top');
    expect(findAdjacentPaneId(layout, 'pane-left-top', 'right')).toBe('pane-right');
    expect(findAdjacentPaneId(layout, 'pane-right', 'left')).toBe('pane-left-top');
  });

  it('returns null when there is no pane in that direction', () => {
    expect(findAdjacentPaneId(layout, 'pane-left-top', 'up')).toBeNull();
    expect(findAdjacentPaneId(layout, 'pane-right', 'right')).toBeNull();
    expect(findAdjacentPaneId(layout, 'missing-pane', 'left')).toBeNull();
  });
});
