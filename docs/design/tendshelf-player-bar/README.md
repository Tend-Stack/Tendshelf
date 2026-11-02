# Tendshelf: desktop player bar with a chapter waveform

Start with `HANDOFF.md`, which has the brief, the specs, the steps and the done-when checklist. Then open `prototype/index.html` in a browser to try the bar.

| Path | What it is |
| --- | --- |
| `HANDOFF.md` | Everything the coding agent needs: what's wrong now, layout, waveform, interaction, server, client, steps, checks |
| `screens/` | The current bug (`00-current-bug.png`), anatomy with measurements, and every state and theme |
| `prototype/index.html` | Working bar with simulated audio (uses `../css` and `../js`) |
| `css/player-bar.css` | The new CSS only; `tokens.css` and `bundle.css` are the full files with it included |
| `js/player-bar.js` | Client module: draws the wave, hover, drag, keyboard, times |
| `go/waveform/` | Loudness values with ffmpeg, a storage helper and the HTTP handler, with tests |
| `templ/player_bar.templ` | Reference templ component (builds with templ v0.3.906) |
