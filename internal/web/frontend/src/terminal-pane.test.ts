import { describe, expect, it } from 'vitest';
import { buildSGRWheelSequence, getWheelRepeatCount, TerminalPane } from './terminal-pane';

const DOM_DELTA_PAGE = 2;

const wheel = (init: Partial<WheelEvent>) => ({
  altKey: false,
  clientX: 0,
  clientY: 0,
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 0,
  shiftKey: false,
  ...init,
}) as WheelEvent;

describe('terminal wheel mouse reporting', () => {
  it('encodes vertical wheel events as SGR mouse reports', () => {
    const event = wheel({ clientX: 25, clientY: 45, deltaY: 10 });

    expect(buildSGRWheelSequence(event, {
      canvasRect: { left: 5, top: 5 },
      cellWidth: 10,
      cellHeight: 20,
      cols: 80,
      rows: 24,
    })).toBe('\x1b[<65;3;3M');
  });

  it('preserves modifiers and horizontal direction', () => {
    const event = wheel({ clientX: 9, clientY: 9, deltaX: -5, deltaY: 1, shiftKey: true, altKey: true, ctrlKey: true });

    expect(buildSGRWheelSequence(event, {
      canvasRect: { left: 0, top: 0 },
      cellWidth: 10,
      cellHeight: 20,
      cols: 80,
      rows: 24,
    })).toBe('\x1b[<94;1;1M');
  });

  it('repeats and clamps page-sized wheel deltas', () => {
    const event = wheel({ deltaY: 1, deltaMode: DOM_DELTA_PAGE });

    expect(getWheelRepeatCount(event, 20, 24)).toBe(5);
  });

  it('returns an empty sequence for zero-sized terminal cells', () => {
    expect(buildSGRWheelSequence(wheel({ deltaY: 10 }), {
      canvasRect: { left: 0, top: 0 },
      cellWidth: 0,
      cellHeight: 20,
      cols: 80,
      rows: 24,
    })).toBe('');
  });
});

describe('terminal socket message handling', () => {
  const createPane = () => {
    const writes: unknown[] = [];
    const pane = Object.create(TerminalPane.prototype) as any;
    pane.id = 'pane-1';
    pane.socket = {} as WebSocket;
    pane.terminal = {
      write(data: unknown, callback?: () => void) {
        writes.push(data);
        callback?.();
      },
      writeln(data: unknown) {
        writes.push(data);
      },
    };
    pane.status = { textContent: '' };
    pane.replayPendingWrites = 0;
    pane.replayCompleteReceived = false;
    pane.suppressInput = true;
    pane.onReadyCalls = 0;
    pane.onExitCalls = 0;
    pane.fitCalls = 0;
    pane.onReady = () => {
      pane.onReadyCalls += 1;
    };
    pane.onStatusCalls = 0;
    pane.onStatus = () => {
      pane.onStatusCalls += 1;
    };
    pane.onExit = () => {
      pane.onExitCalls += 1;
    };
    pane.fitAndResize = () => {
      pane.fitCalls += 1;
    };
    return { pane, writes };
  };

  it('ignores messages from sockets that have been replaced', () => {
    const { pane, writes } = createPane();
    const staleSocket = {} as WebSocket;

    pane.handleMessage(staleSocket, { data: new ArrayBuffer(1) } as MessageEvent);
    pane.handleMessage(staleSocket, { data: JSON.stringify({ type: 'replay-complete' }) } as MessageEvent);
    pane.handleMessage(staleSocket, { data: JSON.stringify({ type: 'ready', id: 'pane-1' }) } as MessageEvent);

    expect(writes).toHaveLength(0);
    expect(pane.replayCompleteReceived).toBe(false);
    expect(pane.replayPendingWrites).toBe(0);
    expect(pane.suppressInput).toBe(true);
    expect(pane.onReadyCalls).toBe(0);
    expect(pane.onStatusCalls).toBe(0);
    expect(pane.fitCalls).toBe(0);
  });

  it('forwards status events from the active socket', () => {
    const { pane } = createPane();
    const socket = pane.socket as WebSocket;

    pane.handleMessage(socket, { data: JSON.stringify({ type: 'status', displayTitle: 'vim foo' }) } as MessageEvent);

    expect(pane.onStatusCalls).toBe(1);
  });

  it('ignores replay write callbacks after socket replacement', () => {
    const { pane } = createPane();
    const socket = pane.socket as WebSocket;
    let replayWriteCallback: (() => void) | undefined;
    pane.terminal.write = (_data: unknown, callback?: () => void) => {
      replayWriteCallback = callback;
    };

    pane.handleMessage(socket, { data: new ArrayBuffer(1) } as MessageEvent);
    expect(pane.replayPendingWrites).toBe(1);

    pane.socket = {} as WebSocket;
    pane.replayPendingWrites = 1;
    pane.replayCompleteReceived = true;
    replayWriteCallback?.();

    expect(pane.replayPendingWrites).toBe(1);
    expect(pane.suppressInput).toBe(true);
  });
});
