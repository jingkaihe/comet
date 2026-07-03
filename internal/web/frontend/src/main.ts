import './styles.css';
import {
  collectPaneIds,
  createTab,
  findAdjacentPaneId,
  newId,
  paneDirectionFromShortcutKey,
  removePaneFromLayout,
  seedCountersFromTabs,
  splitLayout,
  tabIndexFromShortcutKey,
} from './model';
import { defaultTerminalTheme, TerminalPane } from './terminal-pane';
import type { LayoutNode, SplitDirection, TerminalReadyEvent, TerminalStatusResponse, TerminalTab, TerminalTheme, TerminalThemeColors } from './types';

interface PersistedState {
  tabs: TerminalTab[];
  activeTabId: string;
  theme?: string;
  initialized?: boolean;
  version?: number;
}

const DEFAULT_THEME_NAME = 'Comet Warm';
const RUNNING_PROCESS_CLOSE_MESSAGE = 'The terminal still has a running process. If you close the tab the process will be killed';

const themeColorKeys = [
  'foreground',
  'background',
  'cursor',
  'cursorAccent',
  'selectionBackground',
  'selectionForeground',
  'black',
  'red',
  'green',
  'yellow',
  'blue',
  'magenta',
  'cyan',
  'white',
  'brightBlack',
  'brightRed',
  'brightGreen',
  'brightYellow',
  'brightBlue',
  'brightMagenta',
  'brightCyan',
  'brightWhite',
] satisfies Array<keyof TerminalThemeColors>;

const isTerminalThemeColors = (value: unknown): value is TerminalThemeColors => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const colors = value as Record<keyof TerminalThemeColors, unknown>;
  return themeColorKeys.every((key) => typeof colors[key] === 'string' && /^#[\da-f]{6}$/i.test(colors[key]));
};

const isTerminalTheme = (value: unknown): value is TerminalTheme => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const theme = value as Partial<TerminalTheme>;
  return typeof theme.name === 'string' && theme.name.trim().length > 0 && typeof theme.source === 'string' && isTerminalThemeColors(theme.colors);
};

const isLayoutNode = (value: unknown): value is LayoutNode => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const node = value as { type?: unknown; id?: unknown; direction?: unknown; children?: unknown };
  if (node.type === 'pane') {
    return typeof node.id === 'string' && node.id.length > 0;
  }
  return (
    node.type === 'split' &&
    (node.direction === 'vertical' || node.direction === 'horizontal') &&
    Array.isArray(node.children) &&
    node.children.length > 0 &&
    node.children.every(isLayoutNode)
  );
};

const normalizeTab = (value: unknown): TerminalTab | null => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null;
  }
  const tab = value as Partial<TerminalTab>;
  if (typeof tab.id !== 'string' || !isLayoutNode(tab.layout)) {
    return null;
  }
  const panes = collectPaneIds(tab.layout);
  if (panes.length === 0) {
    return null;
  }
  const activePaneId = typeof tab.activePaneId === 'string' && panes.includes(tab.activePaneId)
    ? tab.activePaneId
    : panes[0];

  return {
    id: tab.id,
    title: typeof tab.title === 'string' && tab.title.trim() ? tab.title : '#?',
    customTitle: Boolean(tab.customTitle),
    layout: tab.layout,
    panes,
    activePaneId,
  };
};

class CometApp {
  private readonly root: HTMLElement;
  private readonly tabStrip: HTMLElement;
  private readonly themeSelect: HTMLSelectElement;
  private readonly addButton: HTMLButtonElement;
  private readonly workspace: HTMLElement;
  private readonly hint: HTMLElement;
  private tabs: TerminalTab[] = [];
  private activeTabId = '';
  private activeThemeName = DEFAULT_THEME_NAME;
  private themes: TerminalTheme[] = [{ name: DEFAULT_THEME_NAME, source: 'bundled', colors: defaultTerminalTheme }];
  private layoutVersion = 0;
  private panes = new Map<string, TerminalPane>();

  constructor(root: HTMLElement) {
    this.root = root;
    this.root.className = 'comet-shell';
    this.root.innerHTML = `
      <header class="topbar" aria-label="Terminal tabs">
        <div class="tab-strip" role="tablist"></div>
        <label class="theme-picker" title="Terminal theme">
          <span class="theme-picker-label">Theme</span>
          <select class="theme-select" aria-label="Terminal theme"></select>
        </label>
        <button class="new-tab" type="button" aria-label="New terminal tab" title="New tab"></button>
      </header>
      <main class="workspace" aria-label="Terminal workspace"></main>
      <footer class="hint"></footer>
    `;
    this.tabStrip = this.mustQuery('.tab-strip');
    this.themeSelect = this.mustQuery('.theme-select');
    this.addButton = this.mustQuery('.new-tab');
    this.workspace = this.mustQuery('.workspace');
    this.hint = this.mustQuery('.hint');

    this.themeSelect.addEventListener('change', () => this.selectTheme(this.themeSelect.value));
    this.addButton.addEventListener('click', () => this.addTab());
    window.addEventListener('keydown', (event) => this.handleShortcut(event), { capture: true });
    window.addEventListener('resize', () => this.fitActivePanes());

    this.hint.textContent = navigator.platform.toLowerCase().includes('mac')
      ? '⌘T new tab · ⌘W close tab · ⌘D vertical split · ⌘⇧D horizontal split · ⌘1-9 switch tabs · ⌘⌥←/→/↑/↓ switch panes'
      : 'Ctrl⇧T new tab · Ctrl⇧W close tab · Ctrl⇧D vertical split · Ctrl⌥D horizontal split · Alt1-9 switch tabs · Ctrl⌥←/→/↑/↓ switch panes';
  }

  async start() {
    await this.loadThemes();
    if (!(await this.restoreState())) {
      this.addTab();
      return;
    }
    this.applyThemeToShell();
    this.renderThemeOptions();
    this.render();
  }

  private mustQuery<T extends HTMLElement>(selector: string): T {
    const element = this.root.querySelector<T>(selector);
    if (!element) {
      throw new Error(`missing ${selector}`);
    }
    return element;
  }

  private addTab() {
    const tab = createTab();
    this.tabs.push(tab);
    this.activeTabId = tab.id;
    this.saveState();
    this.render();
    void this.ensurePane(tab.panes[0]).then((pane) => pane.focus());
  }

  private activateTab(tabId: string) {
    this.activeTabId = tabId;
    this.saveState();
    this.render();
    this.fitActivePanes();
  }

  private renameTab(tabId: string) {
    const tab = this.tabs.find((candidate) => candidate.id === tabId);
    if (!tab) {
      return;
    }
    const nextTitle = window.prompt('Tab name', tab.title)?.trim();
    if (!nextTitle) {
      return;
    }
    tab.title = nextTitle;
    tab.customTitle = true;
    this.saveState();
    this.renderTabs();
  }

  private splitActivePane(direction: SplitDirection) {
    const tab = this.activeTab;
    if (!tab) {
      return;
    }
    const paneId = newId('pane');
    tab.layout = splitLayout(tab.layout, tab.activePaneId, direction, paneId);
    tab.panes.push(paneId);
    tab.activePaneId = paneId;
    this.saveState();
    this.render();
    void this.ensurePane(paneId).then((pane) => pane.focus());
  }

  private activateAdjacentPane(direction: ReturnType<typeof paneDirectionFromShortcutKey>) {
    if (!direction) {
      return;
    }

    const tab = this.activeTab;
    if (!tab) {
      return;
    }

    const paneId = findAdjacentPaneId(tab.layout, tab.activePaneId, direction);
    if (!paneId) {
      return;
    }

    this.setActivePane(paneId);
    this.panes.get(paneId)?.focus();
  }

  private get activeTab() {
    return this.tabs.find((tab) => tab.id === this.activeTabId);
  }

  private async ensurePane(paneId: string) {
    let pane = this.panes.get(paneId);
    if (pane) {
      return pane;
    }
    pane = new TerminalPane({
      id: paneId,
      onFocusPane: (id) => this.setActivePane(id),
      onReady: (id, event) => this.updateTitleFromReady(id, event),
      onExit: (id) => this.removePane(id),
      theme: this.currentTheme.colors,
    });
    this.panes.set(paneId, pane);
    await pane.connect();
    return pane;
  }

  private setActivePane(paneId: string) {
    const tab = this.activeTab;
    if (!tab || !tab.panes.includes(paneId)) {
      return;
    }
    tab.activePaneId = paneId;
    this.saveState();
    for (const pane of this.panes.values()) {
      if (pane.id === paneId) {
        pane.element.classList.add('is-active');
      } else {
        pane.blur();
      }
    }
  }

  private updateTitleFromReady(paneId: string, event: TerminalReadyEvent) {
    const tab = this.tabs.find((candidate) => candidate.panes.includes(paneId));
    if (!tab || tab.customTitle || tab.panes[0] !== paneId) {
      return;
    }
    const cwd = event.cwd || '~';
    const userHost = [event.user, event.host].filter(Boolean).join('@');
    tab.title = userHost ? `${userHost}:${cwd}` : cwd;
    this.saveState();
    this.renderTabs();
  }

  private removePane(paneId: string) {
    const tab = this.tabs.find((candidate) => candidate.panes.includes(paneId));
    const pane = this.panes.get(paneId);
    pane?.dispose();
    pane?.element.remove();
    this.panes.delete(paneId);

    if (!tab) {
      return;
    }

    const nextLayout = removePaneFromLayout(tab.layout, paneId);
    if (!nextLayout) {
      this.removeTab(tab.id);
      return;
    }

    tab.layout = nextLayout;
    tab.panes = collectPaneIds(nextLayout);
    if (!tab.panes.includes(tab.activePaneId)) {
      tab.activePaneId = tab.panes[0];
    }
    this.saveState();
    this.render();
  }

  private removeTab(tabId: string) {
    const index = this.tabs.findIndex((tab) => tab.id === tabId);
    if (index === -1) {
      return;
    }
    const [removed] = this.tabs.splice(index, 1);
    for (const paneId of removed.panes) {
      this.panes.get(paneId)?.dispose();
      this.panes.delete(paneId);
    }

    if (this.tabs.length === 0) {
      this.activeTabId = '';
      this.saveState();
      this.render();
      return;
    }

    if (this.activeTabId === tabId) {
      this.activeTabId = this.tabs[Math.max(0, index - 1)]?.id ?? this.tabs[0].id;
    }
    this.saveState();
    this.render();
  }

  private async requestCloseTab(tabId: string) {
    const tab = this.tabs.find((candidate) => candidate.id === tabId);
    if (!tab) {
      return;
    }

    if (await this.tabHasRunningProcess(tab)) {
      const confirmed = window.confirm(RUNNING_PROCESS_CLOSE_MESSAGE);
      if (!confirmed) {
        return;
      }
    }

    await this.terminateTabProcesses(tab);
    this.removeTab(tabId);
  }

  private async tabHasRunningProcess(tab: TerminalTab) {
    const running = await this.fetchRunningPanes(tab.panes);
    if (running === null) {
      return true;
    }
    return tab.panes.some((paneId) => Boolean(running[paneId]));
  }

  private async fetchRunningPanes(paneIds: string[]): Promise<Record<string, boolean> | null> {
    if (paneIds.length === 0) {
      return {};
    }

    try {
      const response = await fetch('/api/terminal/status', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ids: paneIds }),
      });
      if (!response.ok) {
        return null;
      }
      const payload = await response.json() as Partial<TerminalStatusResponse>;
      return payload.running && typeof payload.running === 'object' ? payload.running : null;
    } catch {
      return null;
    }
  }

  private async terminateTabProcesses(tab: TerminalTab) {
    if (tab.panes.length === 0) {
      return;
    }

    try {
      await fetch('/api/terminal/terminate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ids: tab.panes }),
      });
    } catch {
      // The UI tab can still close; any detached server cleanup is best-effort.
    }
  }

  private render() {
    this.renderTabs();
    void this.renderWorkspace();
  }

  private renderTabs() {
    this.tabStrip.replaceChildren();
    this.tabs.forEach((tab, index) => {
      const tabElement = document.createElement('div');
      tabElement.className = `tab ${tab.id === this.activeTabId ? 'is-active' : ''}`;
      tabElement.title = tab.title;

      const button = document.createElement('button');
      button.className = 'tab-main';
      button.type = 'button';
      button.role = 'tab';
      button.ariaSelected = String(tab.id === this.activeTabId);

      const label = document.createElement('span');
      label.className = 'tab-title';
      label.textContent = tab.title;
      const number = document.createElement('span');
      number.className = 'tab-number';
      number.textContent = `#${index + 1}`;
      button.append(label, number);

      const close = document.createElement('button');
      close.className = 'tab-close';
      close.type = 'button';
      close.ariaLabel = `Close ${tab.title}`;
      close.title = 'Close tab';
      close.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M18 6 6 18M6 6l12 12" /></svg>';
      close.addEventListener('click', (event) => {
        event.preventDefault();
        event.stopPropagation();
        void this.requestCloseTab(tab.id);
      });
      tabElement.append(close, button);

      button.addEventListener('click', () => this.activateTab(tab.id));
      button.addEventListener('dblclick', () => this.renameTab(tab.id));
      this.tabStrip.append(tabElement);
    });
  }

  private get currentTheme() {
    return this.themes.find((theme) => theme.name === this.activeThemeName) ?? this.themes[0];
  }

  private async loadThemes() {
    try {
      const response = await fetch('/api/themes');
      if (!response.ok) {
        this.renderThemeOptions();
        return;
      }
      const themes = await response.json() as unknown;
      if (!Array.isArray(themes)) {
        this.renderThemeOptions();
        return;
      }
      const normalized = themes.filter(isTerminalTheme);
      if (normalized.length > 0) {
        this.themes = normalized;
      }
    } catch {
      // Theme loading is best-effort; the bundled default is enough to run.
    }
    this.renderThemeOptions();
  }

  private renderThemeOptions() {
    const activeTheme = this.themes.some((theme) => theme.name === this.activeThemeName)
      ? this.activeThemeName
      : this.themes[0].name;
    this.activeThemeName = activeTheme;
    this.themeSelect.replaceChildren(...this.themes.map((theme) => {
      const option = document.createElement('option');
      option.value = theme.name;
      option.textContent = theme.name;
      return option;
    }));
    this.themeSelect.value = activeTheme;
  }

  private selectTheme(themeName: string) {
    const theme = this.themes.find((candidate) => candidate.name === themeName);
    if (!theme || theme.name === this.activeThemeName) {
      this.themeSelect.value = this.activeThemeName;
      return;
    }
    this.activeThemeName = theme.name;
    this.applyThemeToShell();
    for (const pane of this.panes.values()) {
      pane.setTheme(theme.colors);
    }
    this.saveState();
  }

  private applyThemeToShell() {
    const { colors } = this.currentTheme;
    this.root.style.setProperty('--terminal-bg', colors.background);
    this.root.style.setProperty('--text', colors.foreground);
    this.root.style.setProperty('--accent', colors.cursor);
    this.root.style.setProperty('--theme-selection-bg', colors.selectionBackground);
    this.root.style.setProperty('--theme-selection-fg', colors.selectionForeground);
  }

  private async renderWorkspace() {
    const tab = this.activeTab;
    if (!tab) {
      this.workspace.replaceChildren(this.emptyState());
      return;
    }
    const node = await this.renderLayoutNode(tab.layout);
    this.workspace.replaceChildren(node);
    this.setActivePane(tab.activePaneId);
    this.panes.get(tab.activePaneId)?.focus();
    this.fitActivePanes();
  }

  private async renderLayoutNode(node: LayoutNode): Promise<HTMLElement> {
    if (node.type === 'pane') {
      return (await this.ensurePane(node.id)).element;
    }

    const element = document.createElement('div');
    element.className = `split split-${node.direction}`;
    element.replaceChildren(...(await Promise.all(node.children.map((child) => this.renderLayoutNode(child)))));
    return element;
  }

  private emptyState() {
    const element = document.createElement('section');
    element.className = 'empty-state';
    element.innerHTML = '<p>No terminal tabs are open.</p><button type="button">Open terminal</button>';
    element.querySelector('button')?.addEventListener('click', () => this.addTab());
    return element;
  }

  private fitActivePanes() {
    const tab = this.activeTab;
    if (!tab) {
      return;
    }
    window.requestAnimationFrame(() => {
      tab.panes.forEach((paneId) => this.panes.get(paneId)?.fitAndResize());
    });
  }

  private handleShortcut(event: KeyboardEvent) {
    const key = event.key.toLowerCase();
    const isMac = navigator.platform.toLowerCase().includes('mac');

    const paneDirection = paneDirectionFromShortcutKey(key);
    if (paneDirection !== null) {
      if (isMac && event.metaKey && event.altKey && !event.ctrlKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.activateAdjacentPane(paneDirection);
        return;
      }

      if (!isMac && event.ctrlKey && event.altKey && !event.metaKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.activateAdjacentPane(paneDirection);
        return;
      }
    }

    const tabIndex = tabIndexFromShortcutKey(key);
    if (tabIndex !== null) {
      if (isMac && event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.activateTabByIndex(tabIndex);
        return;
      }

      if (!isMac && event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.activateTabByIndex(tabIndex);
        return;
      }
    }

    if (key === 't') {
      if (isMac && event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.addTab();
        return;
      }

      if (event.ctrlKey && event.shiftKey && !event.altKey && !event.metaKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        this.addTab();
        return;
      }
    }

    if (key === 'w') {
      if (isMac && event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        if (this.activeTabId) {
          void this.requestCloseTab(this.activeTabId);
        }
        return;
      }

      if (event.ctrlKey && event.shiftKey && !event.altKey && !event.metaKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        if (this.activeTabId) {
          void this.requestCloseTab(this.activeTabId);
        }
        return;
      }
    }

    if (key !== 'd') {
      return;
    }

    if (isMac && event.metaKey && !event.ctrlKey && !event.altKey) {
      event.preventDefault();
      event.stopImmediatePropagation();
      this.splitActivePane(event.shiftKey ? 'horizontal' : 'vertical');
      return;
    }

    if (!isMac && event.ctrlKey && event.shiftKey && !event.altKey && !event.metaKey) {
      event.preventDefault();
      event.stopImmediatePropagation();
      this.splitActivePane('vertical');
      return;
    }

    if (!isMac && event.ctrlKey && event.altKey && !event.metaKey) {
      event.preventDefault();
      event.stopImmediatePropagation();
      this.splitActivePane('horizontal');
    }
  }

  private activateTabByIndex(index: number) {
    const tab = this.tabs[index];
    if (!tab) {
      return;
    }
    this.activateTab(tab.id);
  }

  private async restoreState() {
    try {
      const response = await fetch('/api/layout');
      if (!response.ok) {
        return false;
      }
      const parsed = await response.json() as Partial<PersistedState>;
      if (!Array.isArray(parsed.tabs)) {
        return false;
      }
      const tabs = parsed.tabs.map(normalizeTab).filter((tab): tab is TerminalTab => tab !== null);
      if (typeof parsed.theme === 'string' && this.themes.some((theme) => theme.name === parsed.theme)) {
        this.activeThemeName = parsed.theme;
      }
      this.applyThemeToShell();
      this.renderThemeOptions();
      if (tabs.length === 0) {
        if (parsed.initialized) {
          this.tabs = [];
          this.activeTabId = '';
          this.layoutVersion = typeof parsed.version === 'number' ? parsed.version : 0;
          return true;
        }
        return false;
      }
      this.tabs = tabs;
      this.activeTabId = typeof parsed.activeTabId === 'string' && tabs.some((tab) => tab.id === parsed.activeTabId)
        ? parsed.activeTabId
        : tabs[0].id;
      this.layoutVersion = typeof parsed.version === 'number' ? parsed.version : 0;
      seedCountersFromTabs(tabs);
      return true;
    } catch {
      return false;
    }
  }

  private saveState() {
    this.layoutVersion += 1;
    const state: PersistedState = {
      tabs: this.tabs,
      activeTabId: this.activeTabId,
      theme: this.activeThemeName,
      initialized: true,
      version: this.layoutVersion,
    };
    void fetch('/api/layout', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(state),
    }).catch(() => {
      // Layout persistence is best-effort; terminal sessions still run locally.
    });
  }
}

const appRoot = document.querySelector<HTMLElement>('#app');
if (!appRoot) {
  throw new Error('missing app root');
}

void new CometApp(appRoot).start();
