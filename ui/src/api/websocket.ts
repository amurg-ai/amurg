import type { ConnectionState, Envelope } from "@/types";
import { tokenGetter } from "@/api/client";

export type MessageHandler = (env: Envelope) => void;
export type StateChangeHandler = (state: ConnectionState) => void;

interface PendingMessage {
  type: string;
  payload: unknown;
  sessionId?: string;
}

const MAX_PENDING_QUEUE = 50;

export class AmurgSocket {
  private ws: WebSocket | null = null;
  private handlers = new Map<string, Set<MessageHandler>>();
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000;
  private shouldReconnect = true;
  private url: string;
  private onStateChange?: StateChangeHandler;
  private subscriptions = new Map<string, number>(); // sessionId → afterSeq
  private pendingQueue: PendingMessage[] = [];

  constructor() {
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    this.url = `${proto}//${window.location.host}/ws/client`;
  }

  setStateCallback(cb: StateChangeHandler): void {
    this.onStateChange = cb;
  }

  async connect(): Promise<void> {
    const token = await tokenGetter();
    if (!token) return;

    this.ws = new WebSocket(`${this.url}?token=${token}`);

    this.ws.onopen = () => {
      this.reconnectDelay = 1000;
      this.onStateChange?.("connected");

      // Re-subscribe to all tracked sessions
      for (const [sessionId, afterSeq] of this.subscriptions) {
        this.send(
          "client.subscribe",
          { session_id: sessionId, after_seq: afterSeq },
          sessionId
        );
      }

      // Drain pending message queue
      const queue = [...this.pendingQueue];
      this.pendingQueue = [];
      for (const msg of queue) {
        this.send(msg.type, msg.payload, msg.sessionId);
      }
    };

    this.ws.onmessage = (event) => {
      try {
        const env: Envelope = JSON.parse(event.data);

        // Update afterSeq for subscriptions when receiving messages
        if (env.session_id && env.payload) {
          const payload = env.payload as { seq?: number };
          if (typeof payload.seq === "number" && this.subscriptions.has(env.session_id)) {
            const current = this.subscriptions.get(env.session_id) || 0;
            if (payload.seq > current) {
              this.subscriptions.set(env.session_id, payload.seq);
            }
          }
        }

        const handlers = this.handlers.get(env.type);
        if (handlers) {
          handlers.forEach((h) => h(env));
        }
        // Also fire catch-all handlers
        const allHandlers = this.handlers.get("*");
        if (allHandlers) {
          allHandlers.forEach((h) => h(env));
        }
      } catch {
        // Ignore malformed messages
      }
    };

    this.ws.onclose = () => {
      this.onStateChange?.("disconnected");
      if (this.shouldReconnect) {
        this.onStateChange?.("reconnecting");
        setTimeout(() => this.connect(), this.reconnectDelay);
        this.reconnectDelay = Math.min(
          this.reconnectDelay * 2,
          this.maxReconnectDelay
        );
      }
    };

    this.ws.onerror = () => {
      this.ws?.close();
    };
  }

  disconnect(): void {
    this.shouldReconnect = false;
    this.ws?.close();
    this.ws = null;
  }

  on(type: string, handler: MessageHandler): () => void {
    if (!this.handlers.has(type)) {
      this.handlers.set(type, new Set());
    }
    this.handlers.get(type)!.add(handler);
    return () => {
      this.handlers.get(type)?.delete(handler);
    };
  }

  send(type: string, payload: unknown, sessionId?: string): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      // Enqueue user messages and permission responses when not connected
      if (type === "user.message" || type === "permission.response") {
        if (this.pendingQueue.length < MAX_PENDING_QUEUE) {
          this.pendingQueue.push({ type, payload, sessionId });
        }
      }
      return false;
    }

    const env: Envelope = {
      type,
      session_id: sessionId,
      ts: new Date().toISOString(),
      payload,
    };

    this.ws.send(JSON.stringify(env));
    return true;
  }

  sendMessage(sessionId: string, content: string): boolean {
    const messageId = crypto.randomUUID();
    return this.send(
      "user.message",
      {
        session_id: sessionId,
        message_id: messageId,
        content,
      },
      sessionId
    );
  }

  subscribe(sessionId: string, afterSeq = 0): void {
    this.subscriptions.set(sessionId, afterSeq);
    this.send(
      "client.subscribe",
      { session_id: sessionId, after_seq: afterSeq },
      sessionId
    );
  }

  unsubscribe(sessionId: string): void {
    this.subscriptions.delete(sessionId);
    this.send(
      "client.unsubscribe",
      { session_id: sessionId },
      sessionId
    );
  }

  stopSession(sessionId: string): void {
    this.send(
      "stop.request",
      { session_id: sessionId },
      sessionId
    );
  }

  get connected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }
}

// Singleton instance
export const socket = new AmurgSocket();
