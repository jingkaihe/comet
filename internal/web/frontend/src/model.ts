import type { LayoutNode, SplitDirection, TerminalTab } from './types';

let nextId = 1;
let nextTabNumber = 1;

export type PaneNavigationDirection = 'left' | 'right' | 'up' | 'down';

interface PaneRect {
  id: string;
  left: number;
  top: number;
  right: number;
  bottom: number;
  order: number;
}

interface PaneCandidate {
  id: string;
  primaryDistance: number;
  centerDistance: number;
  order: number;
}

const epsilon = 1e-9;

export const newId = (prefix: string) => `${prefix}-${nextId++}`;

export const createTab = (): TerminalTab => {
  const tabNumber = nextTabNumber++;
  const tabId = newId('tab');
  const paneId = newId('pane');

  return {
    id: tabId,
    title: `#${tabNumber}`,
    customTitle: false,
    layout: { type: 'pane', id: paneId },
    panes: [paneId],
    activePaneId: paneId,
  };
};

export const collectPaneIds = (node: LayoutNode): string[] => {
  if (node.type === 'pane') {
    return [node.id];
  }

  return node.children.flatMap((child) => collectPaneIds(child));
};

export const tabIndexFromShortcutKey = (key: string): number | null => {
  if (!/^[1-9]$/.test(key)) {
    return null;
  }

  return Number(key) - 1;
};

export const paneDirectionFromShortcutKey = (key: string): PaneNavigationDirection | null => {
  switch (key.toLowerCase()) {
    case 'arrowleft':
      return 'left';
    case 'arrowright':
      return 'right';
    case 'arrowup':
      return 'up';
    case 'arrowdown':
      return 'down';
    default:
      return null;
  }
};

const appendPaneRects = (
  node: LayoutNode,
  left: number,
  top: number,
  right: number,
  bottom: number,
  rects: PaneRect[]
) => {
  if (node.type === 'pane') {
    rects.push({ id: node.id, left, top, right, bottom, order: rects.length });
    return;
  }

  if (node.children.length === 0) {
    return;
  }

  if (node.direction === 'vertical') {
    const childWidth = (right - left) / node.children.length;
    node.children.forEach((child, index) => {
      appendPaneRects(child, left + childWidth * index, top, left + childWidth * (index + 1), bottom, rects);
    });
    return;
  }

  const childHeight = (bottom - top) / node.children.length;
  node.children.forEach((child, index) => {
    appendPaneRects(child, left, top + childHeight * index, right, top + childHeight * (index + 1), rects);
  });
};

const paneRectsFromLayout = (node: LayoutNode): PaneRect[] => {
  const rects: PaneRect[] = [];
  appendPaneRects(node, 0, 0, 1, 1, rects);
  return rects;
};

const rangeOverlap = (startA: number, endA: number, startB: number, endB: number) => Math.min(endA, endB) - Math.max(startA, startB);

const horizontalCenter = (rect: PaneRect) => (rect.left + rect.right) / 2;

const verticalCenter = (rect: PaneRect) => (rect.top + rect.bottom) / 2;

const candidateForDirection = (active: PaneRect, candidate: PaneRect, direction: PaneNavigationDirection): PaneCandidate | null => {
  let primaryDistance: number;
  let overlap: number;
  let centerDistance: number;

  switch (direction) {
    case 'left':
      primaryDistance = active.left - candidate.right;
      overlap = rangeOverlap(active.top, active.bottom, candidate.top, candidate.bottom);
      centerDistance = Math.abs(verticalCenter(active) - verticalCenter(candidate));
      break;
    case 'right':
      primaryDistance = candidate.left - active.right;
      overlap = rangeOverlap(active.top, active.bottom, candidate.top, candidate.bottom);
      centerDistance = Math.abs(verticalCenter(active) - verticalCenter(candidate));
      break;
    case 'up':
      primaryDistance = active.top - candidate.bottom;
      overlap = rangeOverlap(active.left, active.right, candidate.left, candidate.right);
      centerDistance = Math.abs(horizontalCenter(active) - horizontalCenter(candidate));
      break;
    case 'down':
      primaryDistance = candidate.top - active.bottom;
      overlap = rangeOverlap(active.left, active.right, candidate.left, candidate.right);
      centerDistance = Math.abs(horizontalCenter(active) - horizontalCenter(candidate));
      break;
  }

  if (primaryDistance < -epsilon || overlap <= epsilon) {
    return null;
  }

  return {
    id: candidate.id,
    primaryDistance: Math.max(0, primaryDistance),
    centerDistance,
    order: candidate.order,
  };
};

export const findAdjacentPaneId = (layout: LayoutNode, activePaneId: string, direction: PaneNavigationDirection): string | null => {
  const rects = paneRectsFromLayout(layout);
  const active = rects.find((rect) => rect.id === activePaneId);
  if (!active) {
    return null;
  }

  const candidates = rects
    .filter((rect) => rect.id !== activePaneId)
    .map((rect) => candidateForDirection(active, rect, direction))
    .filter((candidate): candidate is PaneCandidate => candidate !== null)
    .sort((a, b) => a.primaryDistance - b.primaryDistance || a.centerDistance - b.centerDistance || a.order - b.order);

  return candidates[0]?.id ?? null;
};

export const seedCountersFromTabs = (tabs: TerminalTab[]) => {
  const ids = tabs.flatMap((tab) => [tab.id, ...collectPaneIds(tab.layout)]);
  const maxID = ids.reduce((max, id) => {
    const match = /-(\d+)$/.exec(id);
    return match ? Math.max(max, Number(match[1])) : max;
  }, 0);
  nextId = Math.max(nextId, maxID + 1);
  nextTabNumber = Math.max(nextTabNumber, tabs.length + 1);
};

export const splitLayout = (
  node: LayoutNode,
  targetPaneId: string,
  direction: SplitDirection,
  newPaneId: string
): LayoutNode => {
  if (node.type === 'pane') {
    if (node.id !== targetPaneId) {
      return node;
    }

    return {
      type: 'split',
      direction,
      children: [node, { type: 'pane', id: newPaneId }],
    };
  }

  return {
    ...node,
    children: node.children.map((child) => splitLayout(child, targetPaneId, direction, newPaneId)),
  };
};

export const removePaneFromLayout = (node: LayoutNode, paneId: string): LayoutNode | null => {
  if (node.type === 'pane') {
    return node.id === paneId ? null : node;
  }

  const children = node.children
    .map((child) => removePaneFromLayout(child, paneId))
    .filter((child): child is LayoutNode => child !== null);

  if (children.length === 0) {
    return null;
  }
  if (children.length === 1) {
    return children[0];
  }

  return { ...node, children };
};
