import type { LayoutNode, SplitDirection, TerminalTab } from './types';

export const SPLIT_WEIGHT_TOTAL = 100;
export const MIN_SPLIT_WEIGHT = 10;
export const RESIZE_STEP_WEIGHT = 2;

let nextId = 1;
let nextTabNumber = 1;

export type PaneNavigationDirection = 'left' | 'right' | 'up' | 'down';

export type PaneResizeDirection = PaneNavigationDirection;

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

const minSplitWeightForCount = (count: number) => (
  count * MIN_SPLIT_WEIGHT <= SPLIT_WEIGHT_TOTAL ? MIN_SPLIT_WEIGHT : count <= SPLIT_WEIGHT_TOTAL ? 1 : 0
);

const hasValidSizes = (sizes: number[] | undefined, count: number): sizes is number[] => {
  const minimum = minSplitWeightForCount(count);
  return (
    Array.isArray(sizes) &&
    sizes.length === count &&
    sizes.every((size) => Number.isInteger(size) && size >= minimum) &&
    sizes.reduce((sum, size) => sum + size, 0) === SPLIT_WEIGHT_TOTAL
  );
};

export const equalSplitSizes = (count: number): number[] => {
  if (count <= 0) {
    return [];
  }

  const base = Math.floor(SPLIT_WEIGHT_TOTAL / count);
  const remainder = SPLIT_WEIGHT_TOTAL - base * count;
  return Array.from({ length: count }, (_, index) => base + (index < remainder ? 1 : 0));
};

const scaleSplitSizesToTotal = (sizes: number[]): number[] => {
  if (sizes.length === 0) {
    return [];
  }
  if (sizes.length > SPLIT_WEIGHT_TOTAL) {
    return equalSplitSizes(sizes.length);
  }

  const total = sizes.reduce((sum, size) => sum + size, 0);
  if (!Number.isFinite(total) || total <= 0) {
    return equalSplitSizes(sizes.length);
  }

  const exactSizes = sizes.map((size) => (size * SPLIT_WEIGHT_TOTAL) / total);
  const scaledSizes = exactSizes.map((size) => Math.max(1, Math.floor(size)));
  let remainder = SPLIT_WEIGHT_TOTAL - scaledSizes.reduce((sum, size) => sum + size, 0);
  const indexesByFraction = exactSizes
    .map((size, index) => ({ index, fraction: size - Math.floor(size) }))
    .sort((a, b) => b.fraction - a.fraction || a.index - b.index);

  for (let index = 0; remainder > 0; index += 1, remainder -= 1) {
    scaledSizes[indexesByFraction[index % indexesByFraction.length].index] += 1;
  }

  for (let index = scaledSizes.length - 1; remainder < 0; index = (index - 1 + scaledSizes.length) % scaledSizes.length) {
    if (scaledSizes[index] <= 1) {
      continue;
    }
    scaledSizes[index] -= 1;
    remainder += 1;
  }

  return scaledSizes;
};

export const splitSizes = (node: LayoutNode): number[] => {
  if (node.type === 'pane') {
    return [];
  }

  return hasValidSizes(node.sizes, node.children.length)
    ? node.sizes
    : equalSplitSizes(node.children.length);
};

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

  const sizes = splitSizes(node);
  const total = sizes.reduce((sum, size) => sum + size, 0);

  if (node.direction === 'vertical') {
    let childLeft = left;
    node.children.forEach((child, index) => {
      const childRight = index === node.children.length - 1
        ? right
        : childLeft + ((right - left) * sizes[index]) / total;
      appendPaneRects(child, childLeft, top, childRight, bottom, rects);
      childLeft = childRight;
    });
    return;
  }

  let childTop = top;
  node.children.forEach((child, index) => {
    const childBottom = index === node.children.length - 1
      ? bottom
      : childTop + ((bottom - top) * sizes[index]) / total;
    appendPaneRects(child, left, childTop, right, childBottom, rects);
    childTop = childBottom;
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
      sizes: equalSplitSizes(2),
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

  const sizes = splitSizes(node);
  const children: LayoutNode[] = [];
  const nextSizes: number[] = [];
  node.children.forEach((child, index) => {
    const nextChild = removePaneFromLayout(child, paneId);
    if (nextChild === null) {
      return;
    }
    children.push(nextChild);
    nextSizes.push(sizes[index]);
  });

  if (children.length === 0) {
    return null;
  }
  if (children.length === 1) {
    return children[0];
  }

  return { ...node, sizes: scaleSplitSizesToTotal(nextSizes), children };
};

const containsPaneId = (node: LayoutNode, paneId: string): boolean => {
  if (node.type === 'pane') {
    return node.id === paneId;
  }

  return node.children.some((child) => containsPaneId(child, paneId));
};

const resizeAxisForDirection = (direction: PaneResizeDirection): SplitDirection => (
  direction === 'left' || direction === 'right' ? 'vertical' : 'horizontal'
);

const leadingDirectionForSplit = (direction: SplitDirection): PaneResizeDirection => (
  direction === 'vertical' ? 'left' : 'up'
);

const resizeSplitSizes = (sizes: number[], activeIndex: number, direction: PaneResizeDirection): number[] | null => {
  const leadingDirection = leadingDirectionForSplit(resizeAxisForDirection(direction));
  const towardLeadingEdge = direction === leadingDirection;
  let siblingIndex = towardLeadingEdge ? activeIndex - 1 : activeIndex + 1;
  let growsActive = true;
  if (siblingIndex < 0 || siblingIndex >= sizes.length) {
    siblingIndex = towardLeadingEdge ? activeIndex + 1 : activeIndex - 1;
    growsActive = false;
  }
  if (siblingIndex < 0 || siblingIndex >= sizes.length) {
    return null;
  }

  const nextSizes = [...sizes];
  const activeSize = nextSizes[activeIndex];
  const siblingSize = nextSizes[siblingIndex];
  const available = growsActive ? siblingSize - MIN_SPLIT_WEIGHT : activeSize - MIN_SPLIT_WEIGHT;
  const delta = Math.min(RESIZE_STEP_WEIGHT, Math.max(0, available));
  if (delta <= 0) {
    return null;
  }

  if (growsActive) {
    nextSizes[activeIndex] += delta;
    nextSizes[siblingIndex] -= delta;
  } else {
    nextSizes[activeIndex] -= delta;
    nextSizes[siblingIndex] += delta;
  }

  return nextSizes;
};

const resizePaneInLayoutNode = (node: LayoutNode, activePaneId: string, direction: PaneResizeDirection): { node: LayoutNode; changed: boolean; handled: boolean } => {
  if (node.type === 'pane') {
    return { node, changed: false, handled: false };
  }

  const activeChildIndex = node.children.findIndex((child) => containsPaneId(child, activePaneId));
  if (activeChildIndex === -1) {
    return { node, changed: false, handled: false };
  }

  const resizedChild = resizePaneInLayoutNode(node.children[activeChildIndex], activePaneId, direction);
  if (resizedChild.handled) {
    if (!resizedChild.changed) {
      return { node, changed: false, handled: true };
    }
    const children = [...node.children];
    children[activeChildIndex] = resizedChild.node;
    return { node: { ...node, children }, changed: true, handled: true };
  }

  const matchingAxis = resizeAxisForDirection(direction) === node.direction;
  if (matchingAxis) {
    const nextSizes = resizeSplitSizes(splitSizes(node), activeChildIndex, direction);
    if (nextSizes !== null) {
      return { node: { ...node, sizes: nextSizes }, changed: true, handled: true };
    }
    return { node, changed: false, handled: true };
  }

  return { node, changed: false, handled: false };
};

export const resizePaneInLayout = (node: LayoutNode, activePaneId: string, direction: PaneResizeDirection): LayoutNode => {
  const resized = resizePaneInLayoutNode(node, activePaneId, direction);
  return resized.node;
};

export const equalizeLayout = (node: LayoutNode): LayoutNode => {
  if (node.type === 'pane') {
    return node;
  }

  return {
    ...node,
    sizes: equalSplitSizes(node.children.length),
    children: node.children.map(equalizeLayout),
  };
};
