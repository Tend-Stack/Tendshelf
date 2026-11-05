// Throwaway spike for docs/plans/sound-modes.md (S0). Deleted in S1.
// It copies the player's shape on purpose: one <audio>, Media Session handlers, a context built
// inside a tap, and a log that reports what the phone did, including the silence when it froze us.

const audio = document.getElementById("audio");
const statusEl = document.getElementById("status");
const buttons = {
  plain: document.getElementById("plain"),
  chain: document.getElementById("chain"),
  speed1: document.getElementById("speed1"),
  speed15: document.getElementById("speed15"),
  silent: document.getElementById("silent"),
  pause: document.getElementById("pause"),
};

const pageId = Math.random().toString(36).slice(2, 8);
const BOUNDARY_AFTER_SECONDS = 60; // a file change with no tap, as a book's next file
const LOG_EVERY_MS = 5000;

let mode = "plain"; // "plain" | "chain"
let ctx = null;
let analyser = null;
let sourceConnected = false;
let playedSeconds = 0;
let lastTick = 0;
let boundaryDone = false;
let lastRMS = null;

// ---- The Night chain from decision 0022, mono on ----

function buildChain(context) {
  const mono = context.createGain();
  mono.channelCount = 1;
  mono.channelCountMode = "explicit";
  mono.channelInterpretation = "speakers";

  const highpass = context.createBiquadFilter();
  highpass.type = "highpass";
  highpass.frequency.value = 120;
  highpass.Q.value = 0.707;

  const boxiness = context.createBiquadFilter();
  boxiness.type = "peaking";
  boxiness.frequency.value = 250;
  boxiness.Q.value = 1;
  boxiness.gain.value = -3;

  const presence = context.createBiquadFilter();
  presence.type = "peaking";
  presence.frequency.value = 2500;
  presence.Q.value = 1;
  presence.gain.value = 2;

  const shelf = context.createBiquadFilter();
  shelf.type = "highshelf";
  shelf.frequency.value = 6000;
  shelf.gain.value = -6;

  const compressor = context.createDynamicsCompressor();
  compressor.threshold.value = -38;
  compressor.knee.value = 12;
  compressor.ratio.value = 8;
  compressor.attack.value = 0.005;
  compressor.release.value = 0.3;

  const trim = context.createGain();
  trim.gain.value = Math.pow(10, -4 / 20);
  const master = context.createGain();

  analyser = context.createAnalyser();
  analyser.fftSize = 1024;

  mono.connect(highpass);
  highpass.connect(boxiness);
  boxiness.connect(presence);
  presence.connect(shelf);
  shelf.connect(compressor);
  compressor.connect(trim);
  trim.connect(master);
  master.connect(context.destination);
  master.connect(analyser);
  return mono;
}

// Called inside a tap; the context is created before any await so iOS counts the gesture.
async function startChain() {
  if (ctx) {
    await resumeContext();
    return;
  }
  ctx = new AudioContext({ latencyHint: "playback" });
  ctx.addEventListener("statechange", () => report("statechange"));
  const input = buildChain(ctx);
  await resumeContext();
  // Connect the element only once the context runs: a suspended one would swallow the sound.
  if (ctx.state === "running" && !sourceConnected) {
    ctx.createMediaElementSource(audio).connect(input);
    sourceConnected = true;
  }
  mode = "chain";
  render();
  report("chain-built");
}

async function resumeContext() {
  try {
    await ctx.resume();
  } catch (err) {
    note("resume failed: " + err);
  }
}

function rms() {
  if (!analyser) return null;
  const data = new Float32Array(analyser.fftSize);
  analyser.getFloatTimeDomainData(data);
  let sum = 0;
  for (const v of data) sum += v * v;
  lastRMS = Math.sqrt(sum / data.length);
  return lastRMS;
}

// ---- Log: sendBeacon so a page that is hiding still gets its last line out ----

function report(reason) {
  const line = {
    page: pageId,
    reason,
    t: Date.now(),
    device: navigator.userAgent,
    standalone: Boolean(navigator.standalone) || matchMedia("(display-mode: standalone)").matches,
    mode,
    audioSession: navigator.audioSession ? navigator.audioSession.type : null,
    visibility: document.visibilityState,
    ctxState: ctx ? ctx.state : null,
    paused: audio.paused,
    currentTime: audio.currentTime,
    ctxTime: ctx ? ctx.currentTime : null,
    rms: rms(),
    rate: audio.playbackRate,
    src: audio.getAttribute("src"),
    sourceConnected,
  };
  navigator.sendBeacon("/log", new Blob([JSON.stringify(line)], { type: "application/json" }));
  render();
}

function note(text) {
  statusEl.textContent = text + "\n" + statusEl.textContent;
}

function render() {
  buttons.plain.setAttribute("aria-pressed", String(mode === "plain"));
  buttons.chain.setAttribute("aria-pressed", String(mode === "chain"));
  buttons.speed1.setAttribute("aria-pressed", String(audio.playbackRate === 1));
  buttons.speed15.setAttribute("aria-pressed", String(audio.playbackRate === 1.5));
  buttons.plain.disabled = sourceConnected;
  const rmsText = lastRMS === null ? "-" : lastRMS.toFixed(4);
  statusEl.textContent =
    `page ${pageId}  mode ${mode}\n` +
    `context ${ctx ? ctx.state : "none"}  paused ${audio.paused}\n` +
    `time ${audio.currentTime.toFixed(1)} s  played ${playedSeconds.toFixed(0)} s  rms ${rmsText}\n` +
    `source ${audio.getAttribute("src")}  boundary ${boundaryDone ? "done" : "pending"}`;
}

// ---- Media Session, as player.js ----

function play() {
  audio.play().catch((err) => note("play refused: " + err));
}

function pause() {
  audio.pause();
}

if ("mediaSession" in navigator) {
  navigator.mediaSession.metadata = new MediaMetadata({ title: "Sound spike", artist: "Tendshelf", album: "S0" });
  const handlers = {
    play,
    pause,
    seekbackward: () => (audio.currentTime = Math.max(0, audio.currentTime - 15)),
    seekforward: () => (audio.currentTime = audio.currentTime + 30),
  };
  for (const action in handlers) {
    try {
      navigator.mediaSession.setActionHandler(action, handlers[action]);
    } catch {
      // not supported here
    }
  }
}

// ---- File boundary: the src moves from /audio/a to /audio/b at the same place, with no tap ----

function checkBoundary() {
  const now = performance.now();
  if (lastTick && !audio.paused) playedSeconds += (now - lastTick) / 1000;
  lastTick = now;
  if (boundaryDone || playedSeconds < BOUNDARY_AFTER_SECONDS) return;
  boundaryDone = true;
  const place = audio.currentTime;
  const rate = audio.playbackRate;
  audio.addEventListener(
    "loadedmetadata",
    () => {
      audio.currentTime = place;
      audio.playbackRate = rate;
      play();
      report("boundary-resumed");
    },
    { once: true },
  );
  audio.src = "/audio/b";
  report("boundary");
}

// ---- Buttons ----

buttons.plain.addEventListener("click", () => {
  mode = "plain";
  play();
  report("plain");
});
buttons.chain.addEventListener("click", () => {
  const started = startChain(); // runs synchronously up to its first await, inside the tap
  play();
  started.then(() => report("chain-ready"));
});
buttons.speed1.addEventListener("click", () => {
  audio.playbackRate = 1;
  report("speed");
});
buttons.speed15.addEventListener("click", () => {
  audio.playbackRate = 1.5;
  report("speed");
});
buttons.silent.addEventListener("click", () => {
  if (!navigator.audioSession) {
    note("navigator.audioSession does not exist here");
    return;
  }
  const on = buttons.silent.getAttribute("aria-pressed") !== "true";
  navigator.audioSession.type = on ? "playback" : "auto";
  buttons.silent.setAttribute("aria-pressed", String(on));
  report("audioSession");
});
buttons.pause.addEventListener("click", pause);

// Any tap is a chance to wake a context the phone suspended.
document.addEventListener("click", () => {
  if (ctx && ctx.state !== "running") resumeContext();
});

for (const type of ["play", "pause"]) audio.addEventListener(type, () => report(type));
audio.addEventListener("timeupdate", checkBoundary);
document.addEventListener("visibilitychange", () => report("visibilitychange"));
window.addEventListener("pagehide", () => report("pagehide"));
setInterval(() => {
  checkBoundary();
  report("tick");
}, LOG_EVERY_MS);
setInterval(render, 1000);
report("load");
