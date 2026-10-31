# Claude Design prompt: Tendshelf reader (addition)

Give this to Claude Design **after** the main Tendshelf prompt (`claude-design-prompt.md`), in the same project, so it builds on the same design system. Copy everything below the line.

---

## Add the read-along reader to Tendshelf

Tendshelf now lets people add the **ebook** of a book next to its **audiobook**. A model aligns the ebook text to the narration, so the words light up as they're spoken. Design the reader as **part of the same book and the same player**, never as a separate app or a separate library. One book, one place: listen, read, or both, and switch any time without losing your place.

Keep the same design system, themes, tokens, and calm, premium tone. The reader should feel like the most beautiful page you've ever read on a screen, with the voice gently guiding your eyes.

### The core idea: one position, three ways to enjoy a book

- **Listen:** the player as designed.
- **Read along:**
  - The text sits on screen, with the sentence being spoken softly highlighted and the current word subtly emphasized.
  - The page glides to keep the voice in view.
  - Tap any sentence to hear it from there.
- **Read:** just the text, at your own pace, with the audio paused.
  - When you press Play, the narration starts at the sentence you're reading.
  - When you return to Read, the text opens where the audio stopped.
  - Show this "it remembers where you are in both" moment clearly. It's the wow.

Plus two special views:
- **Focus line:** one sentence at a time, very large, for accessibility, dyslexia, or reading in bed.
- **Eyes-free:** a large-control player for driving or walking. Text is hidden, and controls get bigger.

### Screens to design

Phone 390 × 844 for every screen. Tablet 1024 × 768 and desktop 1440 × 900 for the ones marked *.

1. **Book detail, with the ebook added*:**
   - The cover area shows the formats available, as small, elegant format markers ("Audio 11 h 50 m · Ebook · Read-along ready").
   - The primary action stays one tap: "Continue". It opens the mode the person used last.
   - A clear secondary choice: Listen | Read along | Read.
2. **Adding an ebook*:**
   - Drag and drop or pick a file (EPUB, PDF, TXT, DOCX…).
   - Automatic pairing ("This looks like the same book: The Lighthouse Keeper's Daughter, 2nd edition").
   - A manual pairing choice.
   - A friendly refusal for copy-protected files ("This ebook is copy-protected, so Tendshelf can't open it").
3. **Preparing read-along (alignment in progress):**
   - A beautiful progress view: "Matching the text to the narration… chapter 7 of 31".
   - What's ready can already be used ("Chapters 1–6 ready to read along").
   - Gaps are shown honestly, e.g. "Chapter 12 differs from the audio (abridged?), so the text stays readable but won't highlight there."
4. **Read-along, the hero screen*:**
   - Phone: text fills the screen, with a slim player at the bottom: play/pause, ±30 s, scrubber with chapter ticks, speed.
   - Tablet and desktop: an elegant two-page or single wide column, with an optional side panel for chapters, bookmarks, highlights and notes.
   - Highlight styles to show:
     - sentence wash (soft background);
     - word emphasis (weight/underline);
     - both;
     - "karaoke" word fill (optional).
   - A floating **"Back to the voice"** pill appears when the reader scrolls away from the narration, and one tap brings the text back.
   - States: the text where the narration can't be matched shows plainly unhighlighted, with a tiny note.
5. **Read (text only)*:**
   - Page or continuous scroll.
   - A small "Play from here" affordance on the current paragraph.
   - Progress shown both as % and as "about 4 h 10 m of listening left".
6. **Focus line:**
   - One sentence centred, very large, with the next sentence faintly below.
   - Swipe or tap to advance when not listening; when listening, it advances with the voice.
7. **Eyes-free player:**
   - Huge play/pause, ±30 s, sleep timer, speed.
   - High contrast; works with one hand; no text.
8. **Reading settings sheet*:**
   - Font (2–3 curated text faces, including a dyslexia-friendly one), size, line spacing, margins, justification.
   - Theme (light, sepia, dark, night/true black).
   - Highlight style and colour.
   - Auto-scroll on/off and how far ahead the voice stays.
   - Show/hide images.
   - Hyphenation.
   - A live preview inside the sheet.
9. **Highlights, bookmarks and notes, shared between text and audio*:**
   - Select text to highlight or add a note. It also marks the moment in the audio.
   - A bookmark made while listening shows the sentence it belongs to.
   - A timeline list mixing text highlights and audio bookmarks.
   - Export.
   - Each person's notes are private unless shared.
10. **Chapter and contents panel*:**
    - Chapters with both reading progress and listening progress (one combined indicator).
    - Durations, and which chapters are read-along ready.
11. **Sharing an ebook with an invitee:** a separate toggle in the invite sheet ("Also let them read along"), explained in one plain sentence.
12. **Offline:** a downloaded book shows that the text, the audio and the read-along timing are all saved ("Ready offline: listen, read, read along").

### Interaction details

- **The highlight** moves smoothly, word to word (no jitter). Sentence changes cross-fade quickly (under 120 ms).
- **Auto-scroll** keeps the current line about a third from the top. It pauses when the reader scrolls by hand and offers "Back to the voice".
- **Tap a sentence** to play from it. **Long-press** to select text, highlight it, add a note or share a quote.
- **Speed changes** keep the highlight in sync. Pausing leaves the last spoken sentence highlighted.
- **Switching modes** animates the shared text block: Read along → Read keeps the scroll position, and Read → Listen shows the cover expanding from the page.
- **Desktop keys:**
  - Space: play/pause.
  - J / K: previous/next sentence.
  - ← / →: skip.
  - H: highlight.
  - N: note.
  - F: focus line.
  - R: switch Read/Read along.
- **Accessibility:**
  - Screen readers get the text, with the audio paused (and announced).
  - Reduced motion makes the highlight snap instead of slide.
  - Highlight colours meet contrast on every theme.

### Add to the design system

- **Reader tokens:**
  - text column width (max ~68 characters on desktop);
  - reading type scale (body 18–22 px default on phone);
  - line-height presets;
  - highlight colours per theme;
  - selection colours;
  - the "Back to the voice" pill.
- **Components:**
  - reader page;
  - sentence highlight;
  - word emphasis;
  - text selection menu;
  - note bubble;
  - combined progress indicator (read + listened);
  - format markers;
  - pairing card;
  - alignment progress card;
  - reading settings sheet with live preview;
  - mode switcher (Listen | Read along | Read).

### Output

1. High-fidelity screens for all 12, in dark, light and sepia for the hero read-along screen.
2. A short motion spec: the highlight movement, auto-scroll, "Back to the voice", and mode switching.
3. Reader additions to the design system page.
4. Developer handoff notes:
   - which regions are server-rendered partials (chrome, panels, settings) and which part is the small client module that moves the highlight from timestamps;
   - the text layout rules (column, pagination, image handling);
   - accessibility notes.

Use fictional books and text (public-domain-style prose is fine), and no real brands.
