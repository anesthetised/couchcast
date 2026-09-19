import type { ClientMessage, ServerMessage } from "~/protocol";

export type SocketStatus = "connecting" | "open" | "closed" | "kicked";

type Handler = (msg: ServerMessage) => void;

// RoomSocket keeps one WebSocket to a room alive with exponential
// reconnect. Messages are JSON; the session cookie authenticates.
export class RoomSocket {
  private ws: WebSocket | null = null;
  private handlers = new Set<Handler>();
  private attempts = 0;
  private timer: number | null = null;
  private stopped = false;
  status: SocketStatus = "connecting";
  onStatus: (s: SocketStatus) => void = () => {};

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
      this.attempts = 0;
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
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.stopped) {
        if (this.status !== "kicked") this.setStatus("closed");
        return;
      }
      this.setStatus("closed");
      this.scheduleReconnect();
    };
    ws.onerror = () => ws.close();
  }

  private scheduleReconnect() {
    const delay = Math.min(30_000, 500 * 2 ** this.attempts++);
    this.timer = window.setTimeout(() => this.open(), delay);
  }

  private setStatus(s: SocketStatus) {
    this.status = s;
    this.onStatus(s);
  }

  send(msg: ClientMessage): boolean {
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
