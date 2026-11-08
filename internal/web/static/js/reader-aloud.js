// Read aloud for the book reader (decision 0032). reader.js imports this on the first press of the control.
// The device's own voice reads the book one sentence at a time: the spoken sentence is marked (.is-now) and the
// view follows it through reader.js, so the saved place is the top of the view as in any reading.
//
// The engine seam: this module speaks through window.tsNativeSpeech (the Android app, R2) or the browser's
// speechSynthesis. An engine has three methods and only ever offers voices that are on the device:
//   voices()                              -> [{ id, name, lang, isDefault }]
//   speak(text, { voiceId, rate }, ev)    ev = { start(), end(), fail(code), stopped() }; code is "blocked" or "voice"
//   cancel()
// "stopped" means the engine stopped by itself (the app went to the background): the strip shows Paused.

const SENTENCE_MAX = 3000; // longer sentences are split at a space, so no engine meets its long-utterance cut-off
const VOICE_KEY = "ts.aloud.voice";
const RATE_KEY = "ts.aloud.rate";
const TURN_TRIES = 64;
const TURN_WAIT_MS = 300;
const PART_WAIT_MS = 15000;

const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

function remember(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* storage may be blocked: the choice then lasts for this page only */
  }
}

function recall(key) {
  try {
    return localStorage.getItem(key) || "";
  } catch {
    return "";
  }
}

// The browser's engine. Only voices that run on the device (localService) are ever used, and every
// utterance names its voice, so a remote default can never be picked up.
function browserEngine() {
  const synth = window.speechSynthesis;
  const local = () => synth.getVoices().filter((voice) => voice.localService);
  let current = null; // kept referenced: Chromium may drop an utterance's events once it is collected
  return {
    voices: () => local().map((v) => ({ id: v.voiceURI, name: v.name, lang: v.lang, isDefault: v.default })),
    speak(text, { voiceId, rate }, ev) {
      const voice = local().find((v) => v.voiceURI === voiceId);
      if (!voice) return ev.fail("voice");
      current = new SpeechSynthesisUtterance(text);
      current.voice = voice;
      current.lang = voice.lang;
      current.rate = rate;
      current.addEventListener("start", () => ev.start());
      current.addEventListener("end", () => ev.end());
      current.addEventListener("error", (event) => {
        if (event.error === "interrupted" || event.error === "canceled") return;
        ev.fail(event.error === "not-allowed" ? "blocked" : "voice");
      });
      synth.cancel();
      synth.speak(current);
    },
    cancel: () => synth.cancel(),
  };
}

// Pieces of at most SENTENCE_MAX characters, cut at the last space before the limit.
function pieces(text) {
  const list = [];
  let rest = text;
  while (rest.length > SENTENCE_MAX) {
    let cut = rest.lastIndexOf(" ", SENTENCE_MAX);
    if (cut <= 0) cut = SENTENCE_MAX;
    list.push(rest.slice(0, cut));
    rest = rest.slice(cut).trim();
  }
  if (rest) list.push(rest);
  return list;
}

export function aloud(root, shell, kit) {
  const engine = window.tsNativeSpeech || browserEngine();
  const copy = JSON.parse(shell.querySelector("#reader-aloud").dataset.copy || "{}");
  const strip = shell.querySelector("#reader-aloud");
  const openers = shell.querySelectorAll("[data-aloud-src]");
  const control = (name) => strip.querySelector(`[data-aloud="${name}"]`);
  const playButton = control("play");
  const followButton = control("follow");
  const status = strip.querySelector(".ts-aloud__status");
  const voiceSelect = strip.querySelector("#aloud-voice");
  const rateSelect = strip.querySelector("#aloud-rate");
  const done = new AbortController();
  const listen = (target, type, run) => target.addEventListener(type, run, { signal: done.signal });

  let opener = null; // the control that opened the strip, which gets the focus back
  let open = false;
  let paused = false;
  let following = true;
  let current = null; // the sentence marked now
  let serial = 0; // the number of the sentence being spoken; any event of an older one is ignored

  // ---- The strip ----

  const say = (text) => (status.textContent = text || "");

  function setPlayLabel() {
    const label = paused ? copy.play : copy.pause;
    playButton.setAttribute("aria-label", label);
    playButton.querySelector('[data-state="pause"]').hidden = paused;
    playButton.querySelector('[data-state="play"]').hidden = !paused;
  }

  // ---- Voices and speed ----

  const bookLang = () => (root.lang || document.documentElement.lang || "").split("-")[0].toLowerCase();
  const sameLang = (voice) => voice.lang.split(/[-_]/)[0].toLowerCase() === bookLang();
  const byName = (a, b) => a.name.localeCompare(b.name);

  function fillVoices() {
    const list = engine.voices();
    const wanted = voiceSelect.value || recall(VOICE_KEY);
    voiceSelect.replaceChildren(new Option(copy.default, ""));
    const mine = list.filter(sameLang).sort(byName);
    const others = list.filter((voice) => !sameLang(voice)).sort(byName);
    for (const voice of mine) voiceSelect.append(new Option(voice.name, voice.id));
    if (others.length) {
      const group = document.createElement("optgroup");
      group.label = copy.other;
      for (const voice of others) group.append(new Option(voice.name, voice.id));
      voiceSelect.append(group);
    }
    voiceSelect.value = list.some((voice) => voice.id === wanted) ? wanted : "";
  }

  // "" is the device's default voice when it speaks the book's language, else the first voice of the
  // book's language, else the device's default, else the first voice.
  function voiceId() {
    const list = engine.voices();
    if (voiceSelect.value && list.some((voice) => voice.id === voiceSelect.value)) return voiceSelect.value;
    const chosen = list.find((v) => v.isDefault && sameLang(v)) || list.find(sameLang) || list.find((v) => v.isDefault) || list[0];
    return chosen ? chosen.id : "";
  }

  const rate = () => Number(rateSelect.value) || 1;

  // ---- Finding sentences ----

  const sentences = () => [...root.querySelectorAll(".ts-s")];
  const after = (el, step) => sentences()[sentences().indexOf(el) + step] || null;

  // The sentence at the place: the one at the top of the view.
  function atPlace() {
    const sid = kit.place()?.sid;
    return (sid && root.querySelector(`.ts-s[data-sid="${CSS.escape(sid)}"]`)) || root.querySelector(".ts-s");
  }

  function shown(el, whole) {
    const view = root.getBoundingClientRect();
    const box = el.getBoundingClientRect();
    if (whole) return box.top >= view.top - 1 && box.bottom <= view.bottom + 1 && box.left >= view.left - 1 && box.right <= view.right + 1;
    return box.bottom > view.top && box.top < view.bottom && box.right > view.left && box.left < view.right;
  }

  // ---- Speaking ----

  function mark(el) {
    current?.classList.remove("is-now");
    current = el;
    el?.classList.add("is-now");
  }

  function halt() {
    serial++;
    engine.cancel();
  }

  function pause(text) {
    halt();
    paused = true;
    setPlayLabel();
    say(text);
  }

  // Follow the voice while the sentence just finished was in view; else leave the view alone and offer the way back.
  function follow(previous, el) {
    if (previous && previous.isConnected && !shown(previous, false)) following = false;
    if (following && !shown(el, true)) kit.follow(el.dataset.sid);
    followButton.hidden = following;
  }

  function speakAt(el) {
    const previous = current;
    const mine = ++serial;
    mark(el);
    follow(previous, el);
    paused = false;
    setPlayLabel();
    say("");
    const parts = pieces(el.textContent.replace(/\s+/g, " ").trim());
    if (!parts.length) return advance(el, mine);
    const speakPart = (k) => {
      engine.speak(parts[k], { voiceId: voiceId(), rate: rate() }, {
        start() {},
        end() {
          if (mine !== serial) return;
          if (k + 1 < parts.length) speakPart(k + 1);
          else advance(el, mine);
        },
        fail(code) {
          if (mine === serial) pause(code === "blocked" ? copy.blocked : copy.failed);
        },
        stopped() {
          if (mine === serial) pause("");
        },
      });
    };
    speakPart(0);
  }

  // Wait for the next part that the scroll flow fetches by itself.
  function nextPart() {
    return new Promise((finish) => {
      const timer = setTimeout(() => finish(false), PART_WAIT_MS);
      document.addEventListener(
        "htmx:after:swap",
        (event) => {
          if (!root.contains(event.target)) return;
          clearTimeout(timer);
          finish(true);
        },
        { signal: done.signal },
      );
    });
  }

  // The sentence after el, fetching the next part when el is the last of this one. Null when there is none.
  async function nextSentence(el, mine) {
    const next = after(el, 1);
    if (next) return next;
    if (!el.closest("section")?.dataset.nextHref) {
      pause(copy.end);
      return null;
    }
    if (kit.flow() === "pages") {
      for (let tries = 0; tries < TURN_TRIES && el.isConnected; tries++) {
        await kit.turn(1);
        if (mine !== serial) return null;
        if (el.isConnected) await sleep(TURN_WAIT_MS);
        if (mine !== serial) return null;
      }
      if (!el.isConnected) return root.querySelector(".ts-s");
    } else {
      const arrived = nextPart();
      kit.follow(el.dataset.sid);
      await arrived;
      if (mine !== serial) return null;
      const next = after(el, 1);
      if (next) return next;
    }
    pause(copy.noPart);
    return null;
  }

  async function advance(el, mine) {
    const next = await nextSentence(el, mine);
    if (next && mine === serial) speakAt(next);
  }

  function step(direction) {
    if (!open) return;
    const target = after(current, direction);
    if (target) speakAt(target);
  }

  function start() {
    if (window.tsPlayer?.state().playing) window.tsPlayer.pause();
    following = true;
    const el = atPlace();
    if (el) speakAt(el);
  }

  function stop() {
    halt();
    mark(null);
    paused = false;
    say("");
    followButton.hidden = true;
  }

  // ---- Opening and closing ----

  function toggle(button) {
    open = strip.hidden;
    opener = button;
    strip.hidden = !open;
    for (const each of openers) each.setAttribute("aria-expanded", String(open));
    document.dispatchEvent(new Event("ts-reader-look")); // the dock changed height: the reader lays out again
    if (open) {
      fillVoices();
      const stored = recall(RATE_KEY);
      if ([...rateSelect.options].some((o) => o.value === stored)) rateSelect.value = stored;
      playButton.focus();
      start();
    } else {
      stop();
      opener.focus();
    }
  }

  listen(playButton, "click", () => {
    if (paused) speakAt(current || atPlace());
    else pause("");
  });
  listen(control("back"), "click", () => step(-1));
  listen(control("fwd"), "click", () => step(1));
  listen(control("stop"), "click", () => toggle(opener));
  listen(followButton, "click", () => {
    following = true;
    followButton.hidden = true;
    if (current) kit.follow(current.dataset.sid);
  });

  // A change applies from the next sentence: the current one is spoken again with it.
  const changed = () => {
    remember(VOICE_KEY, voiceSelect.value);
    remember(RATE_KEY, rateSelect.value);
    if (open && !paused && current) speakAt(current);
  };
  listen(voiceSelect, "change", changed);
  listen(rateSelect, "change", changed);
  listen(document, "ts:native:voices", fillVoices);
  if (window.speechSynthesis) listen(window.speechSynthesis, "voiceschanged", fillVoices);

  // One sound at a time: the audiobook starting stops the voice.
  listen(document, "ts:player:state", (event) => {
    if (open && !paused && event.detail?.playing) pause("");
  });

  // j goes back one sentence, k forward, only while the strip is open; the guards are reader.js's.
  listen(window, "keydown", (event) => {
    if (!open || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
    if (event.key !== "j" && event.key !== "k") return;
    if (event.target.closest?.("input, textarea, select, [contenteditable], dialog, [role=dialog]")) return;
    if (document.querySelector("#sheet [role=dialog]")) return;
    event.preventDefault();
    step(event.key === "j" ? -1 : 1);
  });

  // Leaving the reader leaves nothing speaking. A boosted link out of the book stops the voice when its request
  // starts (the swap comes a moment later); a sheet link has hx-get and keeps the reader, and the voice, where they are.
  const leave = () => {
    stop();
    done.abort();
  };
  listen(document, "htmx:before:request", (event) => {
    if (!root.contains(event.target) && event.target.matches?.("a[href]:not([hx-get])")) leave();
  });
  listen(document, "htmx:after:swap", () => {
    if (!root.isConnected) leave();
  });
  listen(window, "pagehide", () => engine.cancel());

  return { toggle };
}
