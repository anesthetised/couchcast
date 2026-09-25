// FakeWebSocket stands in for the browser WebSocket in unit tests and
// records every socket the code under test opens.
export class FakeWebSocket {
  static OPEN = 1;
  static all: FakeWebSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string; wasClean: boolean }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeWebSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.drop(1000, "");
  }
  // test helpers
  accept() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }
  deliver(msg: unknown) {
    this.onmessage?.({ data: typeof msg === "string" ? msg : JSON.stringify(msg) });
  }
  drop(code: number, reason: string) {
    this.readyState = 3;
    this.onclose?.({ code, reason, wasClean: code === 1000 });
  }
}
