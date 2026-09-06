/**
 * JSON-Lines transport over a named pipe / unix domain socket.
 *
 * The bridge is the *client* of this pipe, not the server. That is deliberate:
 * on Windows, .NET's `NamedPipeServerStream` can pin an explicit ACL to the
 * current user, while a pipe created from Node/libuv cannot be locked down to
 * the same degree. So the consumer creates and secures the pipe, and the bridge
 * dials in. See ../README.md.
 */
import net from "node:net";
import { Buffer } from "node:buffer";
import type { Frame } from "./contract.js";

export interface PipeChannel {
  send(frame: Frame): void;
  onFrame(handler: (frame: Frame) => void): void;
  onClose(handler: () => void): void;
  close(): void;
}

export async function connectPipe(path: string, timeoutMs: number): Promise<PipeChannel> {
  const socket = await new Promise<net.Socket>((resolve, reject) => {
    const s = net.connect(path);
    const timer = setTimeout(() => {
      s.destroy();
      reject(new Error(`Timed out connecting to pipe ${path} after ${timeoutMs}ms`));
    }, timeoutMs);
    s.once("connect", () => {
      clearTimeout(timer);
      resolve(s);
    });
    s.once("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });

  socket.setNoDelay(true);

  const frameHandlers: Array<(frame: Frame) => void> = [];
  const closeHandlers: Array<() => void> = [];
  let buffer = "";

  socket.on("data", (chunk: Buffer) => {
    buffer += chunk.toString("utf8");
    // A partial line stays in the buffer; only complete lines are dispatched.
    let newlineIndex = buffer.indexOf("\n");
    while (newlineIndex !== -1) {
      const line = buffer.slice(0, newlineIndex).trim();
      buffer = buffer.slice(newlineIndex + 1);
      if (line.length > 0) {
        let parsed: Frame | null = null;
        try {
          parsed = JSON.parse(line) as Frame;
        } catch {
          // A malformed line is the consumer's bug, not a reason to take the
          // process down; drop it and keep the stream usable.
          parsed = null;
        }
        if (parsed) {
          for (const handler of frameHandlers) handler(parsed);
        }
      }
      newlineIndex = buffer.indexOf("\n");
    }
  });

  const notifyClosed = (): void => {
    for (const handler of closeHandlers) handler();
  };
  socket.on("close", notifyClosed);
  socket.on("error", notifyClosed);

  return {
    send(frame: Frame): void {
      if (socket.destroyed) return;
      socket.write(`${JSON.stringify(frame)}\n`);
    },
    onFrame(handler: (frame: Frame) => void): void {
      frameHandlers.push(handler);
    },
    onClose(handler: () => void): void {
      closeHandlers.push(handler);
    },
    close(): void {
      socket.destroy();
    },
  };
}
