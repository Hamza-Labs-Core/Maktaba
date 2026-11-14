// A stub of Google's CAF Web Receiver SDK for the browser tests (story 58.07, T02): served in
// place of www.gstatic.com's cast_receiver_framework.js. It answers the surface the receiver
// uses (src/receiver/caf.ts) and lets the test drive it through window.__caf: a sender's
// load, the player's events, the clock. No video plays; the screens are what is tested.
(function () {
  const listeners = {};
  const custom = {};
  let interceptor = null;
  const state = { time: 0, duration: 0, started: null, sent: [], loads: [], answers: [] };
  const pm = {
    setMessageInterceptor(_type, fn) {
      interceptor = fn;
    },
    addEventListener(type, fn) {
      (Array.isArray(type) ? type : [type]).forEach(
        (t) => (listeners[t] = (listeners[t] || []).concat(fn))
      );
    },
    getCurrentTimeSec: () => state.time,
    getDurationSec: () => state.duration,
    async load(req) {
      state.loads.push(req);
      const out = interceptor ? await interceptor(req) : req;
      state.answers.push(JSON.parse(JSON.stringify(out || null)));
      return out;
    },
  };
  // a Chromecast with Google TV: H.264 and HEVC to 4K, HDR10, Dolby Vision, Atmos
  const video = {
    "avc1.640028": 1080,
    "avc1.4d401f": 720,
    "hev1.1.6.L153.B0": 2160,
    "hev1.2.4.L153.B0": 2160,
    "dvh1.08.06": 2160,
  };
  const ctx = {
    getPlayerManager: () => pm,
    start(options) {
      state.started = options || {};
    },
    canDisplayType(mime, codecs, _w, h) {
      if (mime.indexOf("audio/") === 0) return ["mp4a.40.2", "ac-3", "ec-3"].indexOf(codecs) >= 0;
      return video[codecs] !== undefined && (h || 0) <= video[codecs];
    },
    getDeviceCapabilities: () => ({
      is_hdr_supported: true,
      is_dv_supported: true,
      is_dolby_atmos_supported: true,
    }),
    addCustomMessageListener(ns, fn) {
      custom[ns] = fn;
    },
    sendCustomMessage(ns, senderId, data) {
      state.sent.push({ ns, senderId, data });
    },
  };
  function ErrorData(type) {
    this.type = type;
  }
  window.cast = {
    framework: {
      CastReceiverContext: { getInstance: () => ctx },
      messages: {
        MessageType: { LOAD: "LOAD" },
        HlsSegmentFormat: { TS: "ts", FMP4: "fmp4" },
        HlsVideoSegmentFormat: { MPEG2_TS: "mpeg2_ts", FMP4: "fmp4" },
        StreamType: { BUFFERED: "BUFFERED" },
        ErrorType: { LOAD_FAILED: "LOAD_FAILED" },
        ErrorData,
      },
      events: {
        EventType: {
          PLAYING: "PLAYING",
          PAUSE: "PAUSE",
          BUFFERING: "BUFFERING",
          SEEKED: "SEEKED",
          TIME_UPDATE: "TIME_UPDATE",
          MEDIA_FINISHED: "MEDIA_FINISHED",
          ERROR: "ERROR",
        },
      },
      system: {
        DeviceCapabilities: {
          IS_HDR_SUPPORTED: "is_hdr_supported",
          IS_DV_SUPPORTED: "is_dv_supported",
          IS_DOLBY_ATMOS_SUPPORTED: "is_dolby_atmos_supported",
        },
      },
    },
  };
  // <cast-media-player> is Google's element: a plain one here
  if (!customElements.get("cast-media-player"))
    customElements.define("cast-media-player", class extends HTMLElement {});
  window.__caf = {
    state,
    load: (customData) => pm.load({ media: { contentId: "sender", customData }, autoplay: true }),
    emit(type, extra) {
      (listeners[type] || []).forEach((fn) => fn(Object.assign({ type }, extra || {})));
    },
    at(time, duration) {
      state.time = time;
      if (duration !== undefined) state.duration = duration;
      this.emit("TIME_UPDATE");
    },
    message: (data) => Object.values(custom).forEach((fn) => fn({ senderId: "sender-1", data })),
  };
})();
