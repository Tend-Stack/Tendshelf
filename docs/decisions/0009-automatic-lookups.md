# 0009 Automatic book-detail lookups
Status: proposed (2026-11-03). Changes S4 of `docs/plans/m4-details.md`.
Context: S4 said every lookup needs a click and scans never go online. Owners asked to fill details for new
books and whole libraries without a click per book.
Decision: An owner or admin may turn on "Look up details online for new books" per library (off by default) and
may start "Look up all books" for a library. That explicit choice is the consent. The owner's `online.enabled`
switch still overrides everything. The scan itself never goes online; a separate job, started after a book's
details are read, does. Only title, author, ASIN and ISBN are sent, to Audible and Audnexus unless more sites are chosen.
Automatic results fill gaps only (`online` rows), never override files, sidecars or a person's choice, never
replace a cover, and are applied only on an exact identifier or one unambiguous title-and-author match;
everything else waits for a person. Requests are paced (one book at a time, 4-6 s apart, backoff, a pause
after repeated refusals, at most 2000 requests per site per day).
Consequences: Tendshelf may make requests without a click, which the owner can stop at any time; books can
take days to finish in a large library; the "Scans never go online" sentence in the admin page changes.
