export type SplitDirection = 'vertical' | 'horizontal';

export interface PaneNode {
  type: 'pane';
  id: string;
}

export interface SplitNode {
  type: 'split';
  direction: SplitDirection;
  children: LayoutNode[];
}

export type LayoutNode = PaneNode | SplitNode;

export interface TerminalTab {
  id: string;
  title: string;
  customTitle: boolean;
  layout: LayoutNode;
  panes: string[];
  activePaneId: string;
}

export interface TerminalThemeColors {
  foreground: string;
  background: string;
  cursor: string;
  cursorAccent: string;
  selectionBackground: string;
  selectionForeground: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
}

export interface TerminalTheme {
  name: string;
  source: string;
  colors: TerminalThemeColors;
}

export interface TerminalClientMessage {
  type: 'input' | 'resize' | 'signal';
  data?: string;
  rows?: number;
  cols?: number;
  name?: string;
}

export interface TerminalReadyEvent {
  type: 'ready';
  id: string;
  cwd?: string;
  name?: string;
  pid?: number;
  host?: string;
  user?: string;
}

export interface TerminalExitEvent {
  type: 'exit';
  code: number;
}

export interface TerminalReplayCompleteEvent {
  type: 'replay-complete';
}

export interface TerminalInfoEvent {
  type: 'info' | 'error';
  text?: string;
}

export type TerminalServerEvent =
  | TerminalReadyEvent
  | TerminalExitEvent
  | TerminalReplayCompleteEvent
  | TerminalInfoEvent;
