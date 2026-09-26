import { logEvent } from "~/lib/diagnostics";
import type { ClientCommand, ServerMessage } from "~/protocol";

export type SocketStatus = "connecting" | "open" | "closed" | "kicked";

type Handler = (msg: ServerMessage) => void;

// RoomSocket keeps one WebSocket to a room alive with exponential
// reconnect. Messages are JSON; the session cookie authenticates.
export class RoomSocket {
  private ws: WebSocket | null = null;
  private handlers = new Set<Handler>();
  private timer: number | null = null;
  private stopped = false;
  // attempts counts reconnects since the last successful open; opened
  // reports whether the socket has ever been open (first connect vs. a
  // drop).
  attempts = 0;
  opened = false;
  status: SocketStatus = "connecting";
  onStatus: (s: SocketStatus) => void = () => {};
  // onDrop runs before each reconnect with the server's close reason (empty
  // for network failures); return false to stop reconnecting.
  onDrop: (reason: string, attempts: number) => boolean | Promise<boolean> = () => true;

  constructor(private readonly slug: string) {}

  connect() {
    this.stopped = false;
    this.open();
  }

  private open() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/rooms/${this.slug}/ws`);
    this.ws = ws;
    this.setStatus("connecting");

    ws.onopen = () => {
      if (this.opened) logEvent("socket", `reconnected after ${this.attempts} attempt(s)`);
      this.attempts = 0;
      this.opened = true;
      this.setStatus("open");
    };
    ws.onmessage = (ev) => {
      let msg: ServerMessage;
      try {
        msg = JSON.parse(ev.data as string) as ServerMessage;
      } catch {
        return;
      }
      if (msg.type === "kicked") {
        this.stopped = true;
        this.setStatus("kicked");
      }
      for (const h of this.handlers) h(msg);
    };
    ws.onclose = (ev) => {
      if (this.ws !== ws) return;
      this.ws = null;
      logEvent("socket", `closed ${ev.code}${ev.reason ? `: ${ev.reason}` : ""}`, { wasClean: ev.wasClean });
      if (this.stopped) {
        if (this.status !== "kicked") this.setStatus("closed");
        return;
      }
      // The server closes with a policy-violation code and a reason when
      // it ends the session on purpose (kick, ban, room deleted); the
      // kicked message may not have made it out before the close.
      if (ev.code === 1008 && ev.reason) {
        this.stopped = true;
        this.setStatus("kicked");
        const msg: ServerMessage = { type: "kicked", reason: ev.reason };
        for (const h of this.handlers) h(msg);
        return;
      }
      this.setStatus("closed");
      void this.scheduleReconnect(ev.reason);
    };
    ws.onerror = () => ws.close();
  }

  private async scheduleReconnect(reason: string) {
    const delay = Math.min(30_000, 500 * 2 ** this.attempts++);
    if (!(await this.onDrop(reason, this.attempts))) {
      this.stopped = true;
      return;
    }
    if (this.stopped) return;
    this.timer = window.setTimeout(() => this.open(), delay);
  }

  private setStatus(s: SocketStatus) {
    this.status = s;
    this.onStatus(s);
  }

  send(msg: ClientCommand): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(msg));
    return true;
  }

  subscribe(h: Handler): () => void {
    this.handlers.add(h);
    return () => this.handlers.delete(h);
  }

  close() {
    this.stopped = true;
    if (this.timer !== null) window.clearTimeout(this.timer);
    this.ws?.close();
    this.ws = null;
  }
}
