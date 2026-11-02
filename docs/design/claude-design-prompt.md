# Claude Design prompt: Tendshelf

Copy everything below the line into Claude Design.

---

## Who you are designing for

Design **Tendshelf**, a self-hosted, open-source audiobook app. People run it at home on their own computer or server, put their audiobook collection in it, and listen on their phone, tablet, laptop and TV. They invite family and friends to listen too.

Think of the comfort of the best commercial audiobook apps, the ownership of a self-hosted library, and the calm craft of a beautiful e-reader. People should open it and say "wow": not because it's flashy, but because it's warm, fast and obvious.

The people using it:
- **Maya, 34**, listens on her commute and while cooking. Her phone is in her pocket and earbuds are in. She wants to resume in one tap, skip back 30 s, set a sleep timer, and see how much of the chapter is left.
- **Daniel, 52**, owns 900 audiobooks and runs the server. He wants to find any book in seconds, fix wrong details, see series in order, and invite his kids.
- **Rosa, 71**, Daniel's mother, was invited by link. She has never used an audiobook app. She must be able to press play without help: big targets, plain words, no jargon.

## Design principles

1. **Listening first.** The player is the heart of the product, and resuming is always one tap from anywhere.
2. **Mobile-first, desktop-beautiful.** Design at 390 px wide first. Scale up to tablet (768–1024) and desktop (1280–1920) with real layout changes, not stretched phone screens.
3. **Calm and premium.** Generous space, beautiful covers, restrained color, soft depth, fluid but quick motion (150–250 ms). No clutter, no ads look, no badges everywhere.
4. **Obvious next step.** Every screen has one clear primary action. Plain words ("Continue listening", "Invite someone"), never internal jargon.
5. **Covers are the art.** Let book covers carry color. The interface stays mostly neutral and picks an accent from the current cover for the player.
6. **Accessible by default.** WCAG AA contrast, 44 px minimum touch targets, full keyboard support, screen-reader labels, Dynamic Type and large-text support, reduced-motion support.
7. **Fast and light.** It's built with server-rendered HTML and small partial updates (Go + templ + HTMX), with no heavy client framework. So design for:
   - skeletons over spinners;
   - progressive disclosure;
   - views that work while partially loaded;
   - interactions that are mostly tap → small region updates.

## Visual direction

- **Mood:** a quiet reading room at night, warm lamp light, paper and wood, modern and crisp.
- **Themes:**
  - Dark (default): deep warm charcoal, not pure black.
  - Light: warm off-white, not pure white.
  - "Sepia/Night reading": an optional third theme.
- **Accent:** one brand accent (suggest a warm amber or ember tone; justify your choice). The player additionally takes a muted accent sampled from the current book's cover.
- **Type:**
  - one humanist sans for UI (excellent at small sizes);
  - an optional elegant serif for book titles and the book detail page;
  - tabular numbers for times.
- **Shapes:** soft radius (12–16 px on cards, full pill on primary buttons), subtle 1 px borders, layered shadows only where something floats (mini-player, sheets).
- **Iconography:** one consistent outline set, 1.5–2 px stroke, filled when active.
- **Motion:**
  - the mini-player expands into the full player with a shared-element cover animation;
  - progress rings fill smoothly;
  - nothing bounces for long.
- **Brand mark:** a simple shelf with a sound wave, or a book spine that turns into a waveform. Propose 2 options.

## Information architecture

**Navigation:** phone bottom tabs, tablet side rail, desktop sidebar.
1. **Home:** continue listening, recently added, your series in progress, shared with you, and a "Because you listened to…" row (rule-based, no AI).
2. **Library:**
   - grid or list of all books, with filters (author, narrator, series, genre, length, finished or unfinished, downloaded);
   - sort;
   - a search that feels instant.
3. **Search:** one box across books, authors, narrators, series and your notes, with recent searches.
4. **Downloads:** books saved for offline listening, storage used, and download progress.
5. **Profile:**
   - listening stats (time this week, streaks, finished books — tasteful, not gamey);
   - devices;
   - settings;
   - for admins: Server, Users & invites, Libraries, Metadata.

**Persistent elements:**
- a **mini-player** docked above the bottom tabs on phones and at the bottom of the window on desktop: cover, title, chapter, play/pause, a thin progress line;
- on desktop, the full player can become a right-side panel while browsing.

## Screens to design

Phone at 390 × 844 for all. Tablet at 1024 × 768 and desktop at 1440 × 900 for the ones marked *.

1. **Onboarding / first run (owner)** — 3 steps:
   1. name your server;
   2. point at your audiobook folder (with a friendly scan progress: "Found 412 books… reading covers");
   3. create your account.

   Celebrate the first scan with the covers flowing in.
2. **Accept an invitation (guest)** — Rosa opens a link and sees:
   - "Daniel invited you to listen to 'The Lighthouse Keeper's Daughter' and 120 other books";
   - one big "Start listening" button;
   - optional name and password, passkey, or "continue on this device".
3. **Home*** with all rows above, plus a large "Continue listening" hero card for the current book showing:
   - cover;
   - "Chapter 12 · 18 min left in chapter · 6 h 40 m left in book";
   - a big Play button.
4. **Library*:**
   - grid with covers and progress rings, and a list variant;
   - filter sheet (phone) or filter bar (desktop);
   - an empty state;
   - a "scanning…" state.
5. **Book detail*:**
   - large cover with soft blurred cover color behind;
   - title (serif), author, narrators (tappable), series #, length, year, publisher;
   - Play / Resume, Download, Add to shelf and Share;
   - a description with "more";
   - a chapter list with durations and played state;
   - "More in this series" and "Also narrated by…";
   - **Details** tab: editable metadata with the source of each field (from file, from Audible, edited by you) and a "Look up details online" side-by-side compare sheet where you tick which values to use;
   - ratings from each source shown separately with the date fetched.
6. **Full-screen player*:**
   - big cover with a subtle animated waveform or glow tied to playback;
   - title and chapter;
   - a scrubber with chapter ticks and time labels (elapsed, remaining in chapter, remaining in book);
   - large play/pause, ±30 s / ±15 s (configurable), previous and next chapter;
   - speed (0.5–3.0 ×, with a fine-tune sheet);
   - sleep timer (end of chapter, 15/30/60 min, custom);
   - bookmarks with notes;
   - chapter list sheet, output device and queue.

   It must look stunning on phone and become an elegant two-column layout on desktop (player left; chapters, bookmarks and notes right).
7. **Bookmarks & notes:** a timeline of bookmarks with notes, jump to any, edit notes, export.
8. **Series page** and **Author / Narrator page*:** a hero with photo or initials, books in order with progress, and a "Start series" action.
9. **Search*:** instant results grouped by type, with keyboard navigation on desktop.
10. **Downloads:** per-book download progress, storage meter, auto-download next in series, Wi-Fi-only toggle.
11. **Share & invite*:**
    - invite by link or email;
    - choose what they can access: everything, a library, a collection or one book;
    - role: listener or member, plus admin for owners;
    - expiry and an optional message;
    - see who is listening and revoke access in one tap.

    Make sharing feel generous and safe.
12. **Users & devices (admin)*:** list with role, last active, listening stats, invites pending.
13. **Libraries & scanning (admin)*:** folders, scan now, scan history, problems found (e.g. "4 files still copying", "1 protected file skipped") explained in plain words with a fix.
14. **Settings:** playback defaults, themes, skip intervals, auto-rewind after pause, accessibility (larger text, high contrast, reduced motion), and privacy (no telemetry, stated plainly).

Also design **states**: empty library, no results, offline (cached books still playable, a gentle banner), error (plain words + retry), loading skeletons, and the first-scan celebration.

## Components (design system)

Deliver a compact design system:
- **Tokens:**
  - color: surfaces, text, accent, success, warning, danger, for dark and light;
  - type scale;
  - spacing on an 8 px grid;
  - radii, elevation, motion durations and easings.
- **Components** — show all states (default, hover, focus-visible, pressed, disabled, loading):
  - buttons: primary, secondary, quiet, icon;
  - segmented controls, toggles, chips and filter chips;
  - inputs, search field, select / sheet pickers;
  - progress ring and bar;
  - scrubber with chapter ticks;
  - mini-player and full player controls;
  - book card in grid, list and hero sizes;
  - chapter row, bookmark row, toast, dialog / bottom sheet, tabs, side rail, sidebar, avatar, role badge;
  - empty states;
  - the side-by-side compare table (current value vs online value, tick to use, source badges).

## Interaction details to get right

- **Mini-player:** swipe up to the full player, swipe down to collapse, long-press play for speed.
- **The scrubber:**
  - shows the chapter name while dragging;
  - fine-scrub when dragging up away from the bar;
  - chapter ticks snap lightly.
- **Resume behavior:** gentle auto-rewind of a few seconds after a long pause, with a tiny "rewound 5 s" hint.
- **Sleep timer:** fades out the last 10 s, and shake-to-extend on phones.
- **Lists:** tapping a book in a list never starts playback by accident. Play is its own button.
- **Desktop:** keyboard shortcuts:
  - Space: play / pause;
  - ← / →: skip;
  - [ / ]: speed;
  - B: bookmark;
  - /: search;
  - G then L: library.

## Output I want from you

1. A **moodboard and visual direction** (2 options, then refine the stronger one).
2. **High-fidelity screens** for all the screens above. Use the phone sizes for all of them, and the tablet and desktop sizes for those marked *. Include dark and light for Home, Book detail and Player.
3. The **design system page** (tokens + components with states).
4. A short **motion spec** for the mini-player → full player transition, page transitions and progress animations.
5. **Developer handoff notes:** the layout grid per breakpoint, which regions update independently (so they can be server-rendered partials updated with HTMX), the accessibility notes per component, and the copy for every button and empty state.

Use realistic but fictional book titles, authors, covers and people; no real brands. Keep everything plain-spoken, warm and confident.
