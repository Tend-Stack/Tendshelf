// Test double for Google's Cast Web Sender SDK (decision 0047). Browser tests inject it into the page; it is never served
// by the server and never shipped. It defines only what cast.js calls, and keeps its record in window.__castStub.
(() => {
  const context = { listeners: {}, session: null };
  const remote = { isPaused: false, isMediaLoaded: false, currentTime: 0, mediaInfo: null, playerState: "IDLE" };
  const remoteListeners = {};
  const record = { loads: [], options: null, ended: 0, castState: "NO_DEVICES_AVAILABLE", idle: "", requests: 0 };
  window.__castStub = record;

  const emit = (listeners, type, event) => (listeners[type] || []).forEach((fn) => fn(event || {}));
  const on = (listeners) => (type, fn) => (listeners[type] = (listeners[type] || []).concat(fn));

  window.chrome = window.chrome || {};
  window.chrome.cast = {
    AutoJoinPolicy: { ORIGIN_SCOPED: "origin_scoped" },
    Image: function (url) {
      this.url = url;
    },
    media: {
      DEFAULT_MEDIA_RECEIVER_APP_ID: "CC1AD845",
      StreamType: { BUFFERED: "BUFFERED" },
      RepeatMode: { OFF: "REPEAT_OFF" },
      IdleReason: { ERROR: "ERROR", FINISHED: "FINISHED" },
      PlayerState: { IDLE: "IDLE" },
      MediaInfo: function (contentId, contentType) {
        this.contentId = contentId;
        this.contentType = contentType;
      },
      GenericMediaMetadata: function () {},
      QueueItem: function (media) {
        this.media = media;
      },
      QueueLoadRequest: function (items) {
        this.items = items;
      },
    },
  };

  const session = {
    getCastDevice: () => ({ friendlyName: "Test TV" }),
    getMediaSession: () => ({ idleReason: record.idle }),
    getSessionObj: () => ({
      queueLoad(request, ok) {
        record.loads.push(JSON.parse(JSON.stringify(request)));
        const item = request.items[request.startIndex];
        remote.mediaInfo = item.media;
        remote.isMediaLoaded = true;
        remote.isPaused = false;
        remote.currentTime = item.startTime || 0;
        ok();
      },
    }),
  };

  window.cast = {
    framework: {
      CastState: { NO_DEVICES_AVAILABLE: "NO_DEVICES_AVAILABLE", NOT_CONNECTED: "NOT_CONNECTED" },
      SessionState: { SESSION_STARTED: "SESSION_STARTED", SESSION_ENDED: "SESSION_ENDED" },
      CastContextEventType: { CAST_STATE_CHANGED: "castState", SESSION_STATE_CHANGED: "sessionState" },
      RemotePlayerEventType: {
        CURRENT_TIME_CHANGED: "currentTime",
        IS_PAUSED_CHANGED: "isPaused",
        MEDIA_INFO_CHANGED: "mediaInfo",
        PLAYER_STATE_CHANGED: "playerState",
      },
      CastContext: {
        getInstance: () => ({
          setOptions: (options) => (record.options = options),
          addEventListener: on(context.listeners),
          getCastState: () => record.castState,
          getCurrentSession: () => context.session,
          requestSession: () => {
            record.requests++;
            context.session = session;
            emit(context.listeners, "sessionState", { sessionState: "SESSION_STARTED" });
            return Promise.resolve();
          },
          endCurrentSession: () => {
            context.session = null;
            record.ended++;
            emit(context.listeners, "sessionState", { sessionState: "SESSION_ENDED" });
          },
        }),
      },
      RemotePlayer: function () {
        return remote;
      },
      RemotePlayerController: function () {
        return {
          addEventListener: on(remoteListeners),
          playOrPause() {
            remote.isPaused = !remote.isPaused;
            emit(remoteListeners, "isPaused");
          },
          seek() {
            emit(remoteListeners, "currentTime");
          },
        };
      },
    },
  };

  // Helpers the test calls.
  record.discover = () => {
    record.castState = "NOT_CONNECTED";
    emit(context.listeners, "castState", { castState: "NOT_CONNECTED" });
  };
  record.play = (seconds, paused) => {
    remote.currentTime = seconds;
    remote.isPaused = !!paused;
    emit(remoteListeners, "currentTime");
    emit(remoteListeners, "isPaused");
  };
  record.fail = () => {
    record.idle = "ERROR";
    remote.playerState = "IDLE";
    emit(remoteListeners, "playerState");
  };
})();
