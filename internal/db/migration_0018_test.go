package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

const alSeedEdition = `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + hexA + `', 'tendshelf-ebook-v1', '` + hexB + `', 5, 'epub', 1, 2, 3, 1)`

func alInsert(id, state string) string {
	return `INSERT INTO alignments(id, edition_id, book_id, recipe, audio_key, doc_sha256, state, created_at, updated_at)
VALUES ('` + id + `', 'ed_1', 'bk_1', 'tendshelf-align-v1', '` + hexA + `', '` + hexB + `', '` + state + `', 1, 1)`
}

const alComponent = `INSERT INTO runtime_components(id, kind, name, language, revision, model_sha256, files_json, python_path, worker_sha256, licence_spdx, licence_url, source_url, checked_at)
VALUES ('rc_1', 'emissions-model', 'wav2vec2', 'en', 'r1', '` + hexA + `', '[]', '/py', '` + hexB + `', 'Apache-2.0', 'u', 'u', 1)`

// TestMigration0018Alignments: a v17 database keeps its edition; one active alignment per edition; deleting the
// component clears runtime_id; deleting the edition removes alignments and chapter rows.
func TestMigration0018Alignments(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:17], supported: 17, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	for _, q := range []string{ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""), alSeedEdition} {
		if _, err := prev.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	prev.Close()

	st := openUpTo(t, 18, dir)
	defer st.Close()
	if countRows(t, st.Reader(), "editions") != 1 {
		t.Fatal("edition lost")
	}
	w := st.Writer()
	for _, q := range []string{
		alComponent,
		alInsert("al_1", "done"),
		alInsert("al_2", "queued"),
		`UPDATE alignments SET runtime_id = 'rc_1' WHERE id = 'al_1'`,
		`INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, updated_at) VALUES ('al_1', 0, 'pending', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(alInsert("al_3", "queued")); err == nil {
		t.Error("a second queued alignment must fail (alignments_active)")
	}
	if _, err := w.Exec(`DELETE FROM runtime_components WHERE id = 'rc_1'`); err != nil {
		t.Fatal(err)
	}
	var rt sql.NullString
	if err := st.Reader().QueryRow(`SELECT runtime_id FROM alignments WHERE id = 'al_1'`).Scan(&rt); err != nil || rt.Valid {
		t.Errorf("runtime_id = %v, err %v; want NULL", rt, err)
	}
	if _, err := w.Exec(`DELETE FROM editions WHERE id = 'ed_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "alignments") != 0 || countRows(t, st.Reader(), "alignment_chapters") != 0 {
		t.Error("alignments and chapter rows must go with the edition")
	}
}

// TestMigration0018FromEmpty: the checks and keys hold on a fresh database.
func TestMigration0018FromEmpty(t *testing.T) {
	st := openUpTo(t, 18, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""),
		alSeedEdition,
		alComponent,
		alInsert("al_1", "done"),
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	bad := []struct{ name, q string }{
		{"bad state", alInsert("al_x", "bogus")},
		{"coverage", `UPDATE alignments SET coverage = 1.5 WHERE id = 'al_1'`},
		{"timed > total", `UPDATE alignments SET sentences_timed = 2, sentences_total = 1 WHERE id = 'al_1'`},
		{"start without end", `INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, start_ms, updated_at) VALUES ('al_1', 0, 'aligned', 5, 1)`},
		{"chapter timed > total", `INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, sentences_timed, updated_at) VALUES ('al_1', 0, 'pending', 1, 1)`},
		{"duplicate kind+language", `INSERT INTO runtime_components(id, kind, name, language, revision, model_sha256, files_json, python_path, worker_sha256, licence_spdx, licence_url, source_url, checked_at)
VALUES ('rc_2', 'emissions-model', 'x', 'en', 'r1', '` + hexA + `', '[]', '/py', '` + hexB + `', 'a', 'u', 'u', 1)`},
	}
	for _, c := range bad {
		if _, err := w.Exec(c.q); err == nil {
			t.Errorf("%s must fail", c.name)
		}
	}
}
