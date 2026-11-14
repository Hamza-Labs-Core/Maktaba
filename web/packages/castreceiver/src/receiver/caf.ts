// The part of Google's CAF Web Receiver SDK (cast.framework) the receiver uses, typed by hand:
// Google publishes no npm package of it, and the page loads the SDK from gstatic. The tests run
// against a fake of exactly this surface (test/fakeCaf.ts, e2e/caf-stub.js), so a call added
// here is a call both fakes must answer.

export interface CafMediaMetadata {
  title?: string;
  subtitle?: string;
  images?: { url: string }[];
}

export interface CafTrack {
  trackId: number;
  type: string;
  trackContentId: string;
  trackContentType: string;
  subtype?: string;
  name?: string;
  language?: string;
}

export interface CafMediaInformation {
  contentId?: string;
  contentUrl?: string;
  contentType?: string;
  streamType?: string;
  duration?: number;
  hlsSegmentFormat?: string;
  hlsVideoSegmentFormat?: string;
  metadata?: CafMediaMetadata;
  tracks?: CafTrack[];
  customData?: unknown;
}

export interface CafLoadRequest {
  media: CafMediaInformation;
  currentTime?: number;
  autoplay?: boolean;
  customData?: unknown;
}

export interface CafPlayerManager {
  setMessageInterceptor(type: string, fn: ((req: CafLoadRequest) => unknown) | null): void;
  addEventListener(
    type: string | string[],
    fn: (event: { type: string; [k: string]: unknown }) => void
  ): void;
  getCurrentTimeSec(): number;
  getDurationSec(): number;
  load(req: CafLoadRequest): Promise<void>;
  pause?(): void;
  play?(): void;
}

export interface CafContext {
  getPlayerManager(): CafPlayerManager;
  start(options?: Record<string, unknown>): unknown;
  canDisplayType(
    mimeType: string,
    codecs?: string,
    width?: number,
    height?: number,
    framerate?: number
  ): boolean;
  getDeviceCapabilities?(): Record<string, unknown> | null | undefined;
  addCustomMessageListener(
    namespace: string,
    fn: (event: { senderId: string; data: unknown }) => void
  ): void;
  sendCustomMessage(namespace: string, senderId: string | undefined, data: unknown): void;
}

export interface CafFramework {
  CastReceiverContext: { getInstance(): CafContext };
  messages: {
    MessageType: { LOAD: string };
    HlsSegmentFormat?: Record<string, string>;
    HlsVideoSegmentFormat?: Record<string, string>;
    StreamType?: Record<string, string>;
    TrackType?: Record<string, string>;
    TextTrackType?: Record<string, string>;
    ErrorType?: Record<string, string>;
    ErrorReason?: Record<string, string>;
    ErrorData?: new (type: string) => { reason?: string; customData?: unknown };
  };
  events: { EventType: Record<string, string> };
  system?: { DeviceCapabilities?: Record<string, string> };
}

/** The constant `name` of `table`, or `fallback` (the SDK's own string) when the table is
 * missing: the fakes and an older SDK answer the same. */
export function constant(
  table: Record<string, string> | undefined,
  name: string,
  fallback: string
): string {
  return table?.[name] ?? fallback;
}
