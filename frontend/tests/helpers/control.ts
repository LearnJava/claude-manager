/**
 * ControlClient — thin Node.js client for the control-plane.
 *
 * Provides:
 *  - rpc(method, params)   POST /rpc, returns the result field
 *  - wait(event, match, timeoutMs)  POST /wait, returns the matched event
 *  - subscribe()           returns an async iterator of EnvelopedEvent via WS
 */

import WebSocket from 'ws';

export interface EnvelopedEvent {
  event: string;
  data: Record<string, unknown>;
  ts: string;
}

export interface RpcError {
  code: number;
  message: string;
}

export class ControlClient {
  private readonly base: string;
  private readonly token: string;

  constructor(port: string, token: string) {
    this.base = `http://127.0.0.1:${port}`;
    this.token = token;
  }

  /** Call a JSON-RPC 2.0 method and return its result. Throws on RPC errors. */
  async rpc<T = unknown>(method: string, params: Record<string, unknown>): Promise<T> {
    const res = await fetch(`${this.base}/rpc`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CM-Token': this.token,
      },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
    });

    if (!res.ok) {
      throw new Error(`RPC HTTP ${res.status}: ${await res.text()}`);
    }

    const body = (await res.json()) as { result?: T; error?: RpcError };
    if (body.error) {
      throw new Error(`RPC error ${body.error.code}: ${body.error.message}`);
    }
    return body.result as T;
  }

  /**
   * Block until the first event whose name and data fields match, or throw on
   * timeout.  Checks the ring buffer first (for events already emitted).
   */
  async wait(
    event: string,
    match: Record<string, unknown>,
    timeoutMs = 8_000,
  ): Promise<EnvelopedEvent> {
    const res = await fetch(`${this.base}/wait`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CM-Token': this.token,
      },
      body: JSON.stringify({ event, match, timeout_ms: timeoutMs }),
    });

    if (res.status === 408) {
      throw new Error(
        `wait(${event}) timed out after ${timeoutMs}ms (match=${JSON.stringify(match)})`,
      );
    }
    if (!res.ok) {
      throw new Error(`wait HTTP ${res.status}: ${await res.text()}`);
    }

    const body = await res.json() as EnvelopedEvent;
    // data is a JSON-encoded string from the server
    if (typeof body.data === 'string') {
      (body as unknown as { data: unknown }).data = JSON.parse(body.data as string);
    }
    return body;
  }

  /**
   * Open a WebSocket to /events and return an async iterator of events.
   * The caller should break out of the loop or call the returned close() to
   * stop receiving.
   */
  subscribe(): { events: AsyncIterable<EnvelopedEvent>; close: () => void } {
    const wsUrl = `ws://127.0.0.1:${new URL(this.base).port}/events?token=${encodeURIComponent(this.token)}`;
    const ws = new WebSocket(wsUrl);

    const queue: EnvelopedEvent[] = [];
    const waiters: Array<(ev: EnvelopedEvent | null) => void> = [];
    let closed = false;

    function enqueue(ev: EnvelopedEvent) {
      const waiter = waiters.shift();
      if (waiter) {
        waiter(ev);
      } else {
        queue.push(ev);
      }
    }

    ws.on('message', (raw) => {
      try {
        const env = JSON.parse(raw.toString()) as EnvelopedEvent;
        if (typeof env.data === 'string') {
          (env as unknown as { data: unknown }).data = JSON.parse(env.data as string);
        }
        enqueue(env);
      } catch { /* ignore malformed */ }
    });

    ws.on('close', () => {
      closed = true;
      waiters.forEach((w) => w(null));
      waiters.length = 0;
    });

    const close = () => ws.close();

    const events: AsyncIterable<EnvelopedEvent> = {
      [Symbol.asyncIterator]() {
        return {
          next(): Promise<IteratorResult<EnvelopedEvent>> {
            if (queue.length > 0) {
              return Promise.resolve({ value: queue.shift()!, done: false });
            }
            if (closed) {
              return Promise.resolve({ value: undefined as unknown as EnvelopedEvent, done: true });
            }
            return new Promise<IteratorResult<EnvelopedEvent>>((resolve) => {
              waiters.push((ev) => {
                if (ev === null) {
                  resolve({ value: undefined as unknown as EnvelopedEvent, done: true });
                } else {
                  resolve({ value: ev, done: false });
                }
              });
            });
          },
          return(): Promise<IteratorResult<EnvelopedEvent>> {
            close();
            return Promise.resolve({ value: undefined as unknown as EnvelopedEvent, done: true });
          },
        };
      },
    };

    return { events, close };
  }
}

/** Convenience: create a ControlClient from the env vars set by globalSetup. */
export function clientFromEnv(): ControlClient {
  const port = process.env.CM_CONTROL_PORT ?? '7334';
  const token = process.env.CM_CONTROL_TOKEN;
  if (!token) {
    throw new Error(
      'CM_CONTROL_TOKEN is not set. Run globalSetup first or set the env var.',
    );
  }
  return new ControlClient(port, token);
}
