-- 0004_books_fts: full-text search over books (title, subtitle, authors, narrators, series, description).
-- The index is contentless (results are hydrated from books) and kept by triggers, so no writer can forget it.
CREATE TABLE books_fts_ids (rid INTEGER PRIMARY KEY, book_id TEXT NOT NULL UNIQUE) STRICT;
CREATE VIEW books_fts_src AS SELECT b.id AS book_id, b.title, b.subtitle,
  COALESCE((SELECT group_concat(p.name, ' ') FROM book_people bp JOIN people p ON p.id = bp.person_id
            WHERE bp.book_id = b.id AND bp.role = 'author'), '') AS authors,
  COALESCE((SELECT group_concat(p.name, ' ') FROM book_people bp JOIN people p ON p.id = bp.person_id
            WHERE bp.book_id = b.id AND bp.role = 'narrator'), '') AS narrators,
  COALESCE((SELECT group_concat(s.name, ' ') FROM book_series bs JOIN series s ON s.id = bs.series_id
            WHERE bs.book_id = b.id), '') AS series,
  b.description FROM books b;
CREATE VIRTUAL TABLE books_fts USING fts5(title, subtitle, authors, narrators, series, description,
  content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2', prefix='2 3');
CREATE VIRTUAL TABLE books_fts_vocab USING fts5vocab(books_fts, row);

CREATE TRIGGER books_fts_ai AFTER INSERT ON books BEGIN
  INSERT INTO books_fts_ids(book_id) VALUES (NEW.id);
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.id;
END;
CREATE TRIGGER books_fts_au AFTER UPDATE OF title, subtitle, description ON books BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.id;
END;
CREATE TRIGGER books_fts_ad AFTER DELETE ON books BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = OLD.id);
  DELETE FROM books_fts_ids WHERE book_id = OLD.id;
END;
CREATE TRIGGER books_fts_bp_ai AFTER INSERT ON book_people BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.book_id;
END;
CREATE TRIGGER books_fts_bp_ad AFTER DELETE ON book_people BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = OLD.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = OLD.book_id;
END;
CREATE TRIGGER books_fts_bp_au AFTER UPDATE ON book_people BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = OLD.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = OLD.book_id;
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.book_id;
END;
CREATE TRIGGER books_fts_bs_ai AFTER INSERT ON book_series BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.book_id;
END;
CREATE TRIGGER books_fts_bs_ad AFTER DELETE ON book_series BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = OLD.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = OLD.book_id;
END;
CREATE TRIGGER books_fts_bs_au AFTER UPDATE ON book_series BEGIN
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = OLD.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = OLD.book_id;
  DELETE FROM books_fts WHERE rowid = (SELECT rid FROM books_fts_ids WHERE book_id = NEW.book_id);
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id = NEW.book_id;
END;
CREATE TRIGGER books_fts_people_au AFTER UPDATE OF name ON people BEGIN
  DELETE FROM books_fts WHERE rowid IN (SELECT i.rid FROM books_fts_ids i WHERE i.book_id IN (SELECT book_id FROM book_people WHERE person_id = NEW.id));
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id IN (SELECT book_id FROM book_people WHERE person_id = NEW.id);
END;
CREATE TRIGGER books_fts_series_au AFTER UPDATE OF name ON series BEGIN
  DELETE FROM books_fts WHERE rowid IN (SELECT i.rid FROM books_fts_ids i WHERE i.book_id IN (SELECT book_id FROM book_series WHERE series_id = NEW.id));
  INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
    SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
    FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id WHERE s.book_id IN (SELECT book_id FROM book_series WHERE series_id = NEW.id);
END;

INSERT INTO books_fts_ids(book_id) SELECT id FROM books ORDER BY added_at, id;
INSERT INTO books_fts(rowid, title, subtitle, authors, narrators, series, description)
  SELECT i.rid, s.title, s.subtitle, s.authors, s.narrators, s.series, s.description
  FROM books_fts_src s JOIN books_fts_ids i ON i.book_id = s.book_id;
