import Foundation
@preconcurrency import TendshelfShared

/// The one script the app adds to the page: `window.tsNative`, an event target the page's player scripts talk to (the same
/// shape Android's `addWebMessageListener` gives them: `postMessage(string)` out, `message` events in, `onmessage`).
/// It runs at document start in the page world, in the main frame only, and builds nothing unless the page really is on the
/// connected origin, so another site (or a frame) gets no `tsNative`. User scripts and `evaluateJavaScript` are outside the
/// page's CSP.
nonisolated enum NativeScript {
    static let handlerName = "tsShell"

    static func source(origin: Origin) -> String {
        let origin = ApiPathsKt.jsonString(s: origin.text)
        return """
        (() => {
          const ORIGIN = \(origin);
          if (window.top !== window || location.origin !== ORIGIN || !window.webkit?.messageHandlers?.\(handlerName)) return;
          const handler = window.webkit.messageHandlers.\(handlerName);
          const target = new EventTarget();
          let on = null;
          Object.defineProperty(target, "onmessage", {
            get: () => on,
            set: (f) => { on = typeof f === "function" ? f : null; },
          });
          target.postMessage = (w) => { if (typeof w === "string") handler.postMessage(w); };
          const deliver = (data) => {
            const e = new MessageEvent("message", { data });
            if (on) on(e);
            target.dispatchEvent(e);
          };
          Object.defineProperty(window, "tsNative", { value: target });
          Object.defineProperty(window, "__tsDeliver", { value: deliver });
        })();
        """
    }
}
