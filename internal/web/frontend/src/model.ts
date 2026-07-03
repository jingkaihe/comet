import type { LayoutNode, SplitDirection, TerminalTab } from './types';

let nextId = 1;
let nextTabNumber = 1;

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
