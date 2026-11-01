Shows alignment running on the server. People can leave; it keeps going and sends a toast when done.

- One segment per chapter: teal check colour when done, ember for the one in progress. Chapters that are done can be read along right away.
- Meta in words: "Matching chapter 7 · The Keeper's Log" and "about 6 min left".
- A scan-progress style partial: `hx-trigger="every 2s"` until the server returns the finished card. `role="group"`; the finished state is also announced with a `role="status"` toast: "Read-along is ready for The Lighthouse Keeper's Daughter".
- Stopped partway (`.ts-align--failed`, `role="alert"`): the failed chapter's segment turns `danger` and the card says which chapter and why in words. Chapters before it stay ready to read along; after it the voice plays and the text stays still. Actions: "Try again" and "Choose another ebook".
