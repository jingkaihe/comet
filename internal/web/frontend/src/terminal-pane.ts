import { FitAddon, Ghostty, Terminal } from 'ghostty-web';
import ghosttyWasmUrl from 'ghostty-web/ghostty-vt.wasm?url';
import type { TerminalClientMessage, TerminalReadyEvent, TerminalServerEvent, TerminalThemeColors } from './types';

const FONT_SIZE = 13;
const FONT_FAMILY = '"JetBrains Mono", "SFMono-Regular", Menlo, Monaco, Consolas, "Liberation Mono", "Ubuntu Mono", monospace';
const SGR_MOUSE_MODE = 1006;
const WHEEL_BUTTON_UP = 64;
const WHEEL_BUTTON_DOWN = 65;
const WHEEL_BUTTON_LEFT = 66;
const WHEEL_BUTTON_RIGHT = 67;
const WHEEL_PIXEL_FALLBACK = 33;
const DOM_DELTA_LINE = 1;
const DOM_DELTA_PAGE = 2;

export const defaultTerminalTheme: TerminalThemeColors = {
  background: '#18140f',
  foreground: '#f4eee3',
  cursor: '#d97757',
  cursorAccent: '#171512',
  selectionBackground: '#624733',
  selectionForeground: '#fffaf1',
  black: '#171512',
  red: '#df7c5e',
  green: '#8ea267',
  yellow: '#cfb37a',
  blue: '#7eabd8',
  magenta: '#b795b9',
  cyan: '#87b7b1',
  white: '#efe6d7',
  brightBlack: '#635b4f',
  brightRed: '#f29b80',
  brightGreen: '#a6bf79',
  brightYellow: '#e7c98d',
  brightBlue: '#99c0e6',
  brightMagenta: '#cfadd0',
  brightCyan: '#a5d0ca',
  brightWhite: '#fffaf1',
};

let ghosttyLoadPromise: Promise<Ghostty> | null = null;

const loadGhostty = () => {
  ghosttyLoadPromise ??= Ghostty.load(ghosttyWasmUrl);
  return ghosttyLoadPromise;
};

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max);

export const getWheelRepeatCount = (event: WheelEvent, cellHeight: number, rows: number) => {
  const isHorizontal = Math.abs(event.deltaX) > Math.abs(event.deltaY);
  const delta = isHorizontal ? event.deltaX : event.deltaY;
  if (delta === 0) {
    return 0;
  }

  let unitDelta: number;
  switch (event.deltaMode) {
    case DOM_DELTA_LINE:
      unitDelta = Math.abs(delta);
      break;
    case DOM_DELTA_PAGE:
      unitDelta = Math.abs(delta) * rows;
      break;
    default:
      unitDelta = Math.abs(delta) / Math.max(1, cellHeight || WHEEL_PIXEL_FALLBACK);
      break;
  }

  return clamp(Math.max(1, Math.round(unitDelta)), 1, 5);
};

const getSGRWheelButton = (event: WheelEvent) => {
  if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) {
    return event.deltaX > 0 ? WHEEL_BUTTON_RIGHT : WHEEL_BUTTON_LEFT;
  }
  return event.deltaY > 0 ? WHEEL_BUTTON_DOWN : WHEEL_BUTTON_UP;
};

const getSGRMouseModifiers = (event: WheelEvent) => {
  let modifiers = 0;
  if (event.shiftKey) {
    modifiers += 4;
  }
  if (event.altKey) {
    modifiers += 8;
  }
  if (event.ctrlKey) {
    modifiers += 16;
  }
  return modifiers;
};

export const buildSGRWheelSequence = (event: WheelEvent, options: {
  canvasRect: Pick<DOMRect, 'left' | 'top'>;
  cellWidth: number;
  cellHeight: number;
  cols: number;
  rows: number;
}) => {
  if (options.cellWidth <= 0 || options.cellHeight <= 0) {
    return '';
  }

  const repeatCount = getWheelRepeatCount(event, options.cellHeight, options.rows);
  if (repeatCount === 0) {
    return '';
  }

  const col = clamp(Math.floor((event.clientX - options.canvasRect.left) / options.cellWidth) + 1, 1, options.cols);
  const row = clamp(Math.floor((event.clientY - options.canvasRect.top) / options.cellHeight) + 1, 1, options.rows);
  const button = getSGRWheelButton(event) + getSGRMouseModifiers(event);
  return `\x1b[<${button};${col};${row}M`.repeat(repeatCount);
};

const isTerminalServerEvent = (value: unknown): value is TerminalServerEvent => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const type = (value as { type?: unknown }).type;
  return type === 'ready' || type === 'exit' || type === 'info' || type === 'error' || type === 'replay-complete';
};

export class TerminalPane {
  readonly id: string;
  readonly element: HTMLElement;

  private readonly host: HTMLElement;
  private readonly status: HTMLElement;
  private readonly onFocusPane: (id: string) => void;
  private readonly onReady: (id: string, event: TerminalReadyEvent) => void;
  private readonly onExit: (id: string, code: number) => void;
  private theme: TerminalThemeColors;

  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private socket: WebSocket | null = null;
  private replayPendingWrites = 0;
  private replayCompleteReceived = false;
  private suppressInput = true;
  private resizeObserver: ResizeObserver | null = null;
  private dataDisposable: { dispose: () => void } | null = null;
  private resizeDisposable: { dispose: () => void } | null = null;

  constructor(options: {
    id: string;
    onFocusPane: (id: string) => void;
    onReady: (id: string, event: TerminalReadyEvent) => void;
    onExit: (id: string, code: number) => void;
    theme?: TerminalThemeColors;
  }) {
    this.id = options.id;
    this.onFocusPane = options.onFocusPane;
    this.onReady = options.onReady;
    this.onExit = options.onExit;
    this.theme = options.theme ?? defaultTerminalTheme;
    this.element = document.createElement('section');
    this.element.className = 'terminal-pane';
    this.element.dataset.paneId = this.id;
    this.element.tabIndex = -1;

    this.host = document.createElement('div');
    this.host.className = 'terminal-host';
    this.status = document.createElement('div');
    this.status.className = 'terminal-status';
    this.status.textContent = 'Connecting…';

    this.element.append(this.host, this.status);
    this.element.addEventListener('pointerdown', () => this.focus());
  }

  async connect() {
    const ghostty = await loadGhostty();
    await document.fonts?.load?.(`${FONT_SIZE}px ${FONT_FAMILY}`);

    this.openTerminal(ghostty);

    this.resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => this.fitAndResize());
    this.resizeObserver?.observe(this.element);
    window.setTimeout(() => this.fitAndResize(), 40);
    window.setTimeout(() => this.fitAndResize(), 140);
  }

  private openTerminal(ghostty: Ghostty) {
    this.element.style.background = this.theme.background;
    this.host.style.background = this.theme.background;

    const terminal = new Terminal({
      ghostty,
      allowTransparency: false,
      convertEol: true,
      cursorBlink: true,
      cursorStyle: 'block',
      fontFamily: FONT_FAMILY,
      fontSize: FONT_SIZE,
      scrollback: 5000,
      theme: this.theme,
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(this.host);
    this.terminal = terminal;
    this.fitAddon = fitAddon;

    this.fit();
    this.connectSocket();

    this.dataDisposable = terminal.onData((data) => {
      if (!this.suppressInput) {
        this.sendMessage({ type: 'input', data });
      }
    });
    this.resizeDisposable = terminal.onResize(({ rows, cols }) => {
      this.sendMessage({ type: 'resize', rows, cols });
    });

    terminal.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') {
        return false;
      }
      const key = event.key.toLowerCase();
      if ((event.metaKey || event.ctrlKey) && key === 'c' && terminal.hasSelection()) {
        return false;
      }
      return false;
    });
    terminal.attachCustomWheelEventHandler((event) => this.handleWheel(event));
  }

  focus() {
    this.onFocusPane(this.id);
    this.element.classList.add('is-active');
    this.terminal?.focus();
  }

  blur() {
    this.element.classList.remove('is-active');
  }

  fitAndResize() {
    this.fit();
    if (!this.terminal) {
      return;
    }
    this.sendMessage({ type: 'resize', rows: this.terminal.rows, cols: this.terminal.cols });
  }

  setTheme(theme: TerminalThemeColors) {
    this.theme = theme;
    this.element.style.background = theme.background;
    this.host.style.background = theme.background;
    void this.reopenTerminal();
  }

  dispose() {
    this.resizeObserver?.disconnect();
    this.dataDisposable?.dispose();
    this.resizeDisposable?.dispose();
    this.socket?.close();
    this.terminal?.dispose();
  }

  private async reopenTerminal() {
    if (!this.terminal) {
      return;
    }
    const ghostty = await loadGhostty();

    this.suppressInput = true;
    this.replayPendingWrites = 0;
    this.replayCompleteReceived = false;
    this.status.textContent = 'Retinting…';

    const socket = this.socket;
    this.socket = null;
    socket?.close();
    this.dataDisposable?.dispose();
    this.resizeDisposable?.dispose();
    this.terminal.dispose();
    this.terminal = null;
    this.fitAddon = null;
    this.host.replaceChildren();

    this.openTerminal(ghostty);
    this.fitAndResize();
  }

  private fit() {
    if (!this.terminal || !this.fitAddon) {
      return;
    }
    const dimensions = this.fitAddon.proposeDimensions();
    if (!dimensions || Number.isNaN(dimensions.cols) || Number.isNaN(dimensions.rows)) {
      return;
    }
    const cols = Math.max(2, dimensions.cols);
    const rows = Math.max(1, dimensions.rows);
    if (this.terminal.cols !== cols || this.terminal.rows !== rows) {
      this.terminal.resize(cols, rows);
    }
  }

  private connectSocket() {
    if (!this.terminal) {
      return;
    }
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = new URL('/api/terminal/ws', `${protocol}//${window.location.host}`);
    url.searchParams.set('id', this.id);
    url.searchParams.set('rows', String(this.terminal.rows));
    url.searchParams.set('cols', String(this.terminal.cols));

    const token = new URLSearchParams(window.location.search).get('token');
    if (token) {
      url.searchParams.set('token', token);
    }

    const socket = new WebSocket(url);
    socket.binaryType = 'arraybuffer';
    this.socket = socket;

    socket.addEventListener('open', () => {
      if (this.socket !== socket) {
        return;
      }
      this.fitAndResize();
    });
    socket.addEventListener('message', (event) => this.handleMessage(socket, event));
    socket.addEventListener('close', () => {
      if (this.socket !== socket) {
        return;
      }
      this.status.textContent = this.status.textContent.startsWith('Exited') ? this.status.textContent : 'Disconnected';
    });
    socket.addEventListener('error', () => {
      if (this.socket !== socket) {
        return;
      }
      this.status.textContent = 'Connection failed';
      this.element.classList.add('has-error');
    });
  }

  private sendMessage(message: TerminalClientMessage) {
    if (this.socket?.readyState !== WebSocket.OPEN) {
      return;
    }
    this.socket.send(JSON.stringify(message));
  }

  private handleWheel(event: WheelEvent) {
    if (
      this.suppressInput ||
      !this.terminal?.wasmTerm?.isAlternateScreen() ||
      !this.terminal.wasmTerm.hasMouseTracking() ||
      !this.terminal.wasmTerm.getMode(SGR_MOUSE_MODE, false)
    ) {
      return false;
    }

    const canvas = this.terminal.renderer?.getCanvas();
    const metrics = this.terminal.renderer?.getMetrics();
    if (!canvas || !metrics) {
      return false;
    }

    const sequence = buildSGRWheelSequence(event, {
      canvasRect: canvas.getBoundingClientRect(),
      cellWidth: metrics.width,
      cellHeight: metrics.height,
      cols: this.terminal.cols,
      rows: this.terminal.rows,
    });
    if (!sequence) {
      return false;
    }

    event.preventDefault();
    event.stopPropagation();
    this.sendMessage({ type: 'input', data: sequence });
    return true;
  }

  private releaseReplaySuppressionIfReady() {
    if (!this.replayCompleteReceived || this.replayPendingWrites > 0) {
      return;
    }
    this.suppressInput = false;
    this.status.textContent = '';
  }

  private handleMessage(socket: WebSocket, event: MessageEvent) {
    if (this.socket !== socket || !this.terminal) {
      return;
    }

    const terminal = this.terminal;
    if (typeof event.data === 'string') {
      try {
        const payload = JSON.parse(event.data) as unknown;
        if (!isTerminalServerEvent(payload)) {
          return;
        }
        if (payload.type === 'ready') {
          this.status.textContent = 'Restoring…';
          this.onReady(this.id, payload);
          this.fitAndResize();
          return;
        }
        if (payload.type === 'replay-complete') {
          this.replayCompleteReceived = true;
          this.releaseReplaySuppressionIfReady();
          return;
        }
        if (payload.type === 'exit') {
          this.onExit(this.id, payload.code);
          return;
        }
        if ((payload.type === 'info' || payload.type === 'error') && payload.text) {
          terminal.writeln(`\r\n${payload.text}`);
        }
      } catch {
        terminal.writeln(`\r\n${event.data}`);
      }
      return;
    }

    if (event.data instanceof ArrayBuffer) {
      const data = new Uint8Array(event.data);
      if (!this.replayCompleteReceived) {
        this.replayPendingWrites += 1;
        terminal.write(data, () => {
          if (this.socket !== socket) {
            return;
          }
          this.replayPendingWrites = Math.max(0, this.replayPendingWrites - 1);
          this.releaseReplaySuppressionIfReady();
        });
        return;
      }
      terminal.write(data);
    }
  }
}
