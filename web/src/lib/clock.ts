import type { RoomSocket } from "~/lib/ws";

// ClockSync estimates the offset between the browser clock and the server
// clock from ping/pong round trips. offset = serverTime - localTime, taken
// as the median of recent samples so a single slow round trip does not
// skew it.
export class ClockSync {
  private samples: { offset: number; rtt: number }[] = [];
  private timer: number | null = null;
  private burst = 0;
  offset = 0;
  rtt = 0;
  onUpdate: () => void = () => {};

  constructor(private readonly socket: RoomSocket) {}

  start() {
    this.samples = [];
    this.burst = 5;
    this.tick();
  }

  stop() {
    if (this.timer !== null) window.clearTimeout(this.timer);
    this.timer = null;
  }

  private tick() {
    this.socket.send({ type: "ping", t0: Date.now() });
    const delay = this.burst > 0 ? 300 : 5000;
    if (this.burst > 0) this.burst--;
    this.timer = window.setTimeout(() => this.tick(), delay);
  }

  handlePong(t0: number, t1: number) {
    const t2 = Date.now();
    const rtt = t2 - t0;
    // Server time was t1 at roughly the midpoint of the round trip.
    const offset = t1 - (t0 + rtt / 2);
    this.samples.push({ offset, rtt });
    if (this.samples.length > 9) this.samples.shift();

    const sorted = [...this.samples].sort((a, b) => a.offset - b.offset);
    const mid = sorted[Math.floor(sorted.length / 2)];
    if (mid) {
      this.offset = mid.offset;
      this.rtt = mid.rtt;
    }
    this.onUpdate();
  }

  // serverNow returns the estimated server time in unix ms.
  serverNow(): number {
    return Date.now() + this.offset;
  }
}
