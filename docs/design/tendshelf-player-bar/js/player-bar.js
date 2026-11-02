/*
 * Tendshelf desktop player bar: chapter waveform scrubber.
 * No dependencies. Load once; it survives HTMX swaps because the bar is hx-preserve.
 *
 *   const bar = TendshelfPlayerBar.mount(document.getElementById('player-bar'), {
 *     audio: document.getElementById('audio'),   // the one <audio> element
 *     chapter: { index: 12, title: 'The Second Lamp', start: 34020, end: 35958 }, // seconds in the book
 *     peaksUrl: (ch) => `/books/42/peaks?from=${ch.start}&to=${ch.end}`,
 *     peaksRate: 5,                              // values per second the server returns
 *     bookDuration: 63132,                       // seconds, for the book progress hairline
 *   });
 *   bar.setChapter(nextChapter);                 // call when the chapter changes
 */
(function (global) {
  'use strict';

  var BAR_PITCH = 4;      // px between bar centres (2px bar, 2px gap)
  var WAVE_HEIGHT = 36;   // px, matches .ts-wave height
  var MIN_BAR = 2;        // px, quietest bar still visible

  function fmt(sec) {
    sec = Math.max(0, Math.round(sec));
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    var mm = h ? String(m).padStart(2, '0') : String(m);
    return (h ? h + ':' : '') + mm + ':' + String(s).padStart(2, '0');
  }
  function words(sec) {
    sec = Math.max(0, Math.round(sec));
    var m = Math.floor(sec / 60), s = sec % 60;
    if (m === 0) return s + ' s';
    return m + ' min' + (s ? ' ' + s + ' s' : '');
  }
  function spoken(sec) {
    sec = Math.max(0, Math.round(sec));
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60, out = [];
    if (h) out.push(h + (h === 1 ? ' hour' : ' hours'));
    if (m) out.push(m + (m === 1 ? ' minute' : ' minutes'));
    if (!h) out.push(s + (s === 1 ? ' second' : ' seconds'));
    return out.join(' ');
  }

  // Downsample loudness values (0–255) to `bars` bars: the mean of each bucket, then stretched so this
  // chapter's quietest tenth sits at 20% height and its loudest bar at full height. A chapter of steady
  // narration is nearly flat in absolute terms; the stretch shows its shape (pauses, quiet scenes,
  // raised voices) instead of decibels. Returns an SVG path of vertical lines in a bars*PITCH × HEIGHT box.
  function wavePath(values, bars, flat) {
    var n = values.length, means = new Array(bars);
    for (var i = 0; i < bars; i++) {
      var a = Math.floor(i * n / bars), b = Math.max(a + 1, Math.floor((i + 1) * n / bars)), sum = 0, c = 0;
      for (var j = a; j < b && j < n; j++) { sum += values[j]; c++; }
      means[i] = c ? sum / c : 0;
    }
    var sorted = means.slice().sort(function (x, y) { return x - y; });
    var lo = sorted[Math.floor(bars * 0.1)], hi = sorted[bars - 1] || 1, span = Math.max(1, hi - lo);
    var d = '', mid = WAVE_HEIGHT / 2;
    for (var k = 0; k < bars; k++) {
      var f = flat ? 0.14 : 0.2 + 0.8 * Math.min(1, Math.max(0, (means[k] - lo) / span));
      var h = Math.max(MIN_BAR, f * (WAVE_HEIGHT - 4));
      var x = k * BAR_PITCH + BAR_PITCH / 2;
      d += 'M' + x.toFixed(1) + ' ' + (mid - h / 2).toFixed(1) + 'V' + (mid + h / 2).toFixed(1);
    }
    return d;
  }
  function flatValues(n) { var a = new Uint8Array(n); a.fill(36); return a; }

  function mount(root, opts) {
    var audio = opts.audio;
    var wave = root.querySelector('.ts-wave');
    var paths = wave.querySelectorAll('svg path');
    var svgs = wave.querySelectorAll('svg');
    var input = wave.querySelector('.ts-wave__input');
    var tip = wave.querySelector('.ts-wave__tip');
    var tElapsed = root.querySelector('[data-pbar="elapsed"]');
    var tLeft = root.querySelector('[data-pbar="left"]');
    var tChapter = root.querySelector('[data-pbar="chapter"]');
    var bookFill = root.querySelector('.ts-pbar__book-progress > span');
    var playBtn = root.querySelector('.ts-pbar__play');

    var ch = opts.chapter, values = null, bars = 0, dragging = false, dragTime = 0, raf = 0, lastSpoken = 0;

    function len() { return Math.max(1, ch.end - ch.start); }
    function timeAtX(clientX) {
      var r = wave.getBoundingClientRect();
      var f = Math.min(1, Math.max(0, (clientX - r.left) / r.width));
      return ch.start + f * len();
    }
    function pct(t) { return Math.min(100, Math.max(0, (t - ch.start) / len() * 100)); }

    function draw() {
      var w = wave.clientWidth;
      var nb = Math.max(24, Math.floor(w / BAR_PITCH));
      if (nb === bars && !draw.dirty) return;
      bars = nb; draw.dirty = false;
      var d = wavePath(values || flatValues(nb), nb, !values);
      for (var i = 0; i < svgs.length; i++) svgs[i].setAttribute('viewBox', '0 0 ' + nb * BAR_PITCH + ' ' + WAVE_HEIGHT);
      for (var k = 0; k < paths.length; k++) paths[k].setAttribute('d', d);
    }

    function show(t) {
      wave.style.setProperty('--p', pct(t) + '%');
      if (tElapsed) tElapsed.textContent = fmt(t - ch.start);
      if (tLeft) tLeft.textContent = '−' + fmt(ch.end - t);
      if (bookFill && opts.bookDuration) bookFill.style.width = (t / opts.bookDuration * 100) + '%';
      if (!dragging) input.value = String(Math.round(t - ch.start));
      var now = Date.now();
      if (document.activeElement === input || now - lastSpoken > 10000) {
        input.setAttribute('aria-valuetext', spoken(t - ch.start) + ' of ' + spoken(len()) + ', chapter ' + ch.index + ', ' + ch.title);
        lastSpoken = now;
      }
    }

    function tick() {
      if (!dragging) show(audio.currentTime);
      raf = audio.paused ? 0 : requestAnimationFrame(tick);
    }
    function startTick() { if (!raf) raf = requestAnimationFrame(tick); }

    function seek(t) {
      t = Math.min(ch.end, Math.max(ch.start, t));
      audio.currentTime = t;
      show(t);
    }

    function setTip(target) {
      var delta = target - audio.currentTime;
      var rel = Math.abs(delta) < 1 ? '' : '<small>' + words(Math.abs(delta)) + (delta > 0 ? ' ahead' : ' back') + '</small>';
      tip.innerHTML = fmt(target - ch.start) + (dragging ? '<small>chapter ' + ch.index + '</small>' : rel);
    }

    // Pointer: hover previews, press-and-drag scrubs, a click jumps.
    wave.addEventListener('pointerenter', function () { wave.classList.add('is-hover'); });
    wave.addEventListener('pointerleave', function () { if (!dragging) wave.classList.remove('is-hover'); });
    wave.addEventListener('pointermove', function (e) {
      var t = timeAtX(e.clientX);
      wave.style.setProperty('--h', pct(t) + '%');
      if (dragging) { dragTime = t; show(t); }
      setTip(t);
    });
    wave.addEventListener('pointerdown', function (e) {
      if (e.button !== 0) return;
      e.preventDefault();
      wave.setPointerCapture(e.pointerId);
      dragging = true; dragTime = timeAtX(e.clientX);
      wave.classList.add('is-dragging');
      show(dragTime); setTip(dragTime);
    });
    function endDrag(e) {
      if (!dragging) return;
      dragging = false;
      wave.classList.remove('is-dragging');
      if (e.type === 'pointerup') seek(dragTime);
      if (!wave.matches(':hover')) wave.classList.remove('is-hover');
    }
    wave.addEventListener('pointerup', endDrag);
    wave.addEventListener('pointercancel', endDrag);

    // Keyboard and assistive tech go through the real range input.
    input.addEventListener('input', function () { if (!dragging) seek(ch.start + Number(input.value)); });
    input.addEventListener('keydown', function (e) {
      var t = audio.currentTime, step = { ArrowLeft: -5, ArrowRight: 5, ArrowDown: -5, ArrowUp: 5, PageDown: -30, PageUp: 30 }[e.key];
      if (step !== undefined) { e.preventDefault(); seek(t + step); }
      else if (e.key === 'Home') { e.preventDefault(); seek(ch.start); }
      else if (e.key === 'End') { e.preventDefault(); seek(ch.end - 1); }
    });

    if (playBtn) playBtn.addEventListener('click', function () { audio.paused ? audio.play() : audio.pause(); });
    // Skip buttons may cross into the next or previous chapter; onChapterEnd/setChapter handles that.
    root.querySelectorAll('[data-skip]').forEach(function (b) {
      b.addEventListener('click', function () { audio.currentTime = Math.max(0, audio.currentTime + Number(b.dataset.skip)); show(audio.currentTime); });
    });
    audio.addEventListener('play', function () { if (playBtn) { playBtn.setAttribute('aria-label', 'Pause'); playBtn.innerHTML = PAUSE_ICON; } startTick(); });
    audio.addEventListener('pause', function () { if (playBtn) { playBtn.setAttribute('aria-label', 'Play'); playBtn.innerHTML = PLAY_ICON; } show(audio.currentTime); });
    audio.addEventListener('seeked', function () { show(audio.currentTime); });
    audio.addEventListener('timeupdate', function () {
      if (audio.currentTime >= ch.end && opts.onChapterEnd) opts.onChapterEnd(ch);
      if (audio.paused) show(audio.currentTime);
    });

    new ResizeObserver(function () { draw(); }).observe(wave);

    function load() {
      values = null; draw.dirty = true;
      wave.classList.add('ts-wave--loading');
      if (tChapter) tChapter.textContent = 'Drawing the waveform for chapter ' + ch.index + '…';
      draw();
      var url = opts.peaksUrl(ch), tries = 0;
      (function attempt() {
        fetch(url).then(function (r) {
          if (r.status === 202 && tries++ < 20) { setTimeout(attempt, 3000); return null; } // server still computing
          if (!r.ok) throw new Error('peaks ' + r.status);
          return r.arrayBuffer();
        }).then(function (buf) {
          if (!buf) return;
          values = new Uint8Array(buf); draw.dirty = true; draw();
          wave.classList.remove('ts-wave--loading');
          if (tChapter) tChapter.textContent = 'Chapter ' + ch.index + ' · ' + ch.title;
        }).catch(function () {
          // No waveform: keep the flat bars. Scrubbing still works.
          wave.classList.remove('ts-wave--loading');
          if (tChapter) tChapter.textContent = 'Chapter ' + ch.index + ' · ' + ch.title;
        });
      })();
    }

    load(); show(audio.currentTime); if (!audio.paused) startTick();
    input.max = String(Math.round(len()));

    return {
      setChapter: function (next) { ch = next; input.max = String(Math.round(len())); load(); show(audio.currentTime); },
      redraw: function () { draw.dirty = true; draw(); }
    };
  }

  var PLAY_ICON = '<svg class="ts-icon ts-icon--fill" viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5.5v13a1 1 0 0 0 1.5.86l10.5-6.5a1 1 0 0 0 0-1.72L9.5 4.64A1 1 0 0 0 8 5.5z"></path></svg>';
  var PAUSE_ICON = '<svg class="ts-icon ts-icon--fill" viewBox="0 0 24 24" aria-hidden="true"><rect x="6" y="5" width="4" height="14" rx="1"></rect><rect x="14" y="5" width="4" height="14" rx="1"></rect></svg>';

  global.TendshelfPlayerBar = { mount: mount, wavePath: wavePath, fmt: fmt };
})(window);
