/**
 * Batches timeline events per agent before they cross the pipe.
 *
 * One codex turn emits many small events. Sending a frame each would make the
 * pipe the bottleneck; dropping intermediates would silently lose assistant
 * text. So: buffer for a short window, then send everything buffered in one
 * frame. Only when a consumer falls far enough behind to exceed the cap do we
 * drop, and then we say how many — the consumer's cue to refetch, since the live
 * stream is for immediacy and an authoritative fetch is for correctness.
 */
import type { TimelineBatch, TimelineEvent } from "./contract.js";

export interface TimelineBufferOptions {
  windowMs: number;
  maxBuffered: number;
  emit(batch: TimelineBatch): void;
  /** Injectable for tests; defaults to the real timer. */
  schedule?: (fn: () => void, ms: number) => { unref?(): void };
}

interface PendingBatch {
  events: TimelineEvent[];
  dropped: number;
}

export class TimelineBuffer {
  private readonly options: TimelineBufferOptions;
  private readonly pending = new Map<string, PendingBatch>();
  private timer: { unref?(): void } | null = null;

  public constructor(options: TimelineBufferOptions) {
    this.options = options;
  }

  public push(event: TimelineEvent): void {
    let batch = this.pending.get(event.agentId);
    if (!batch) {
      batch = { events: [], dropped: 0 };
      this.pending.set(event.agentId, batch);
    }

    batch.events.push(event);
    if (batch.events.length > this.options.maxBuffered) {
      const overflow = batch.events.length - this.options.maxBuffered;
      batch.events.splice(0, overflow);
      batch.dropped += overflow;
    }

    this.arm();
  }

  private arm(): void {
    if (this.timer) return;
    const schedule = this.options.schedule ?? ((fn, ms) => setTimeout(fn, ms));
    const handle = schedule(() => {
      this.timer = null;
      this.flush();
    }, this.options.windowMs);
    // Never keep the process alive just to deliver a batch.
    handle.unref?.();
    this.timer = handle;
  }

  /** Sends everything buffered. Safe to call when empty. */
  public flush(): void {
    for (const [agentId, batch] of this.pending) {
      if (batch.events.length === 0 && batch.dropped === 0) continue;
      this.options.emit({
        agentId,
        events: batch.events,
        ...(batch.dropped > 0 ? { dropped: batch.dropped } : {}),
      });
    }
    this.pending.clear();
  }

  /** Forgets buffered events for an agent, e.g. after unsubscribe or archive. */
  public forget(agentId: string): void {
    this.pending.delete(agentId);
  }
}
