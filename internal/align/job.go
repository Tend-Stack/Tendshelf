package align

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"

	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
)

// Hold codes: a person decides what happens next (docs/read-along.md §2).
const (
	HoldWorkerFailed   = "worker_failed"
	HoldRuntimeChanged = "runtime_changed"
	HoldAudioChanged   = "audio_changed"
)

// Failure codes besides CodeNoMatch.
const (
	codeEditionGone      = "edition_gone"
	codeTextUnreadable   = "text_unreadable"
	codeAudioUnavailable = "audio_unavailable"
	codeRuntimeMissing   = "runtime_missing"
	codeDecodeFailed     = "decode_failed"
	codeBadPayload       = "bad_payload"
)

const (
	emitAttempts   = 3  // a window that fails this many times holds the job
	leftAfter      = 10 // windows measured in this run before leftMs is shown
	samplesPerMs   = 16
	bytesPerMs     = samplesPerMs * 2
	minWindowBytes = 400 * 2 // fewer samples than one model frame is silence
)

// errGone ends a run quietly: the alignment is no longer active (cancelled or discarded).
var errGone = errors.New("align: alignment no longer active")

// progress is the job's progress value.
type progress struct {
	Stage         string `json:"stage"`
	ChaptersDone  int    `json:"chaptersDone"`
	ChaptersTotal int    `json:"chaptersTotal"`
	AudioDoneMs   int64  `json:"audioDoneMs"`
	AudioTotalMs  int64  `json:"audioTotalMs"`
	LeftMs        *int64 `json:"leftMs,omitempty"`
}

// run is one execution of the handler.
type run struct {
	s     *Service
	t     *jobs.Task
	ctx   context.Context
	row   Alignment
	ed    ebook.Edition
	files []library.AudioFile
	comp  alignrt.Component
	lang  string
	vocab Vocab
	em    alignrt.Emitter

	prog     progress
	emitted  int // windows emitted in this run
	started  time.Time
	winTotal int
	winDone  int
}

func (s *Service) hook(ctx context.Context, stage string, n int) {
	if s.d.Hook != nil {
		s.d.Hook(ctx, stage, n)
	}
}

// handle is the align.book handler.
func (s *Service) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p payload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.AlignmentID == "" {
		return nil, &jobs.Error{Code: codeBadPayload, Text: "The alignment request is malformed.", Permanent: true}
	}
	row, ok, err := s.store.Get(ctx, p.AlignmentID)
	if err != nil || !ok {
		return nil, err
	}
	if row.JobID == "" {
		// Enqueue returned and the handler won the race to name its job.
		if _, err := s.store.SetJob(ctx, row.ID, t.ID); err != nil {
			return nil, err
		}
		if row, ok, err = s.store.Get(ctx, p.AlignmentID); err != nil || !ok {
			return nil, err
		}
	}
	switch {
	case row.JobID != t.ID:
		return nil, nil
	case row.State != StateQueued && row.State != StateEmitting && row.State != StateAligning && row.State != StateHeld:
		return nil, nil
	}
	r := &run{s: s, t: t, ctx: ctx, row: row, started: time.Now()}
	err = r.execute()
	if errors.Is(err, errGone) {
		return nil, nil
	}
	return nil, err
}

// stopped returns the cancellation cause when the job context is done.
func (r *run) stopped() error {
	if r.ctx.Err() != nil {
		return context.Cause(r.ctx)
	}
	return nil
}

// settle moves the row to state; a row that is no longer active ends the run.
func (r *run) settle(state, code string) error {
	ok, err := r.s.store.SetState(r.ctx, r.row.ID, r.row.JobID, state, code)
	if err != nil {
		return err
	}
	if !ok {
		return errGone
	}
	return nil
}

// fail ends the alignment for good with a code.
func (r *run) fail(code, text string) error {
	if err := r.settle(StateFailed, code); err != nil {
		return err
	}
	return &jobs.Error{Code: code, Text: text, Permanent: true}
}

// hold parks the alignment for a person; the runner keeps the job held without spending an attempt.
func (r *run) hold(code, text string) error {
	if err := r.settle(StateHeld, code); err != nil {
		return err
	}
	return &jobs.Error{Code: code, Text: text, Hold: true}
}

func (r *run) report() {
	r.prog.AudioDoneMs = min(r.prog.AudioDoneMs, r.prog.AudioTotalMs)
	r.prog.LeftMs = nil
	if r.emitted >= leftAfter && r.winTotal > r.winDone {
		per := time.Since(r.started) / time.Duration(r.emitted)
		left := (per * time.Duration(r.winTotal-r.winDone)).Milliseconds()
		r.prog.LeftMs = &left
	}
	r.t.Progress(r.prog)
}

func (r *run) execute() error {
	s, ctx := r.s, r.ctx
	var ok bool
	var err error
	if r.ed, ok, err = s.d.Ebooks.Edition(ctx, r.row.EditionID); err != nil {
		return err
	} else if !ok {
		return r.fail(codeEditionGone, "The ebook for this alignment is gone.")
	}
	doc, err := ebook.ReadDoc(s.textDir(), r.ed.DocSHA256)
	if err != nil {
		if errors.Is(err, ebook.ErrDocCorrupt) || errors.Is(err, ebook.ErrDocTooLarge) {
			return r.fail(codeTextUnreadable, "Tendshelf cannot read the ebook text.")
		}
		return err
	}
	var key string
	if r.files, key, err = s.audioSet(ctx, r.row.BookID); errors.Is(err, ErrAudioUnavailable) {
		return r.fail(codeAudioUnavailable, "The audio files are missing, unreadable or copy-protected.")
	} else if err != nil {
		return err
	}
	r.lang = s.editionLang(ctx, r.ed, r.row.BookID)
	if r.comp, ok, err = s.d.Runtimes.Usable(ctx, r.lang); err != nil {
		return err
	} else if !ok {
		return r.fail(codeRuntimeMissing, "The alignment runtime is not available for this language.")
	}
	switch {
	case r.row.State == StateHeld:
		// A person resumed: take the current audio and model.
		adopted, err := s.store.Adopt(ctx, r.row.ID, r.row.JobID, key, r.comp.ModelSHA256, r.comp.ID)
		if err != nil {
			return err
		}
		if !adopted {
			return nil
		}
		r.row.AudioKey, r.row.ModelSHA256 = key, r.comp.ModelSHA256
	case key != r.row.AudioKey:
		return r.hold(HoldAudioChanged, "The audio files changed while Tendshelf was working. Continue with the new files, or discard this.")
	case r.comp.ModelSHA256 != r.row.ModelSHA256:
		return r.hold(HoldRuntimeChanged, "The alignment model changed. Continue with the new model, or discard this.")
	}
	var ident alignrt.Identity
	if err := json.Unmarshal([]byte(r.comp.IdentityJSON), &ident); err != nil {
		return r.fail(codeRuntimeMissing, "The alignment runtime has not been checked.")
	}
	if r.vocab, err = NewVocab(ident.Vocab, ident.Blank, ident.Delimiter); err != nil {
		return r.fail(codeRuntimeMissing, "The alignment runtime has not been checked.")
	}

	if err := r.emitStage(); err != nil {
		return err
	}
	return r.alignStage(doc)
}

// windows is the number of 30 s windows of a file.
func windows(f library.AudioFile) int { return int((f.DurationMs + WindowMs - 1) / WindowMs) }

func (r *run) emitStage() error {
	s := r.s
	if err := r.settle(StateEmitting, ""); err != nil {
		return err
	}
	r.prog.Stage = StateEmitting
	for _, f := range r.files {
		n := windows(f)
		r.winTotal += n
		r.prog.AudioTotalMs += f.DurationMs
		for k := 0; k < n; k++ {
			if s.cache.Has(f.QuickHash, r.row.ModelSHA256, k) {
				r.winDone++
				r.prog.AudioDoneMs += windowMs(f, k)
			}
		}
		s.cache.Touch(f.QuickHash, r.row.ModelSHA256)
	}
	r.report()
	for fi, f := range r.files {
		n := windows(f)
		for k := 0; k < n; {
			if s.cache.Has(f.QuickHash, r.row.ModelSHA256, k) {
				k++
				continue
			}
			b := k
			for b+1 < n && !s.cache.Has(f.QuickHash, r.row.ModelSHA256, b+1) {
				b++
			}
			if err := r.emitRun(fi, f, k, b); err != nil {
				return err
			}
			k = b + 1
		}
	}
	// Audio that changed under the job makes the windows just cached the wrong ones.
	for _, f := range r.files {
		h, _, err := s.d.Hash(f.Path)
		if err != nil || h != f.QuickHash {
			return r.hold(HoldAudioChanged, "An audio file changed while Tendshelf was working. Continue with the new files, or discard this.")
		}
	}
	return nil
}

// windowMs is the length of window k of file f in milliseconds.
func windowMs(f library.AudioFile, k int) int64 { return min(WindowMs, f.DurationMs-int64(k)*WindowMs) }

// emitRun decodes windows a..b of file f in one ffmpeg stream and caches their emissions.
func (r *run) emitRun(fi int, f library.AudioFile, a, b int) error {
	fromMs := max(0, int64(a)*WindowMs-OverlapMs)
	st := r.s.openStream(r.ctx, f.Path, fromMs, int64(b-a+1)*WindowMs+2*OverlapMs)
	defer st.close()
	for k := a; k <= b; k++ {
		if err := r.stopped(); err != nil {
			return err
		}
		reqStart := max(0, int64(k)*WindowMs-OverlapMs)
		reqEnd := min(int64(k+1)*WindowMs+OverlapMs, f.DurationMs)
		pcm, err := st.window((reqStart-fromMs)*bytesPerMs, (reqEnd-fromMs)*bytesPerMs)
		if err != nil {
			if cerr := r.stopped(); cerr != nil {
				return cerr
			}
			r.s.d.Log.Warn("align: decoding failed", "alignment", r.row.ID, "err", err)
			return &jobs.Error{Code: codeDecodeFailed, Text: "Tendshelf could not read the audio.", Err: err}
		}
		em := Em{Vocab: r.vocab.Size(), FrameMs: FrameMs, Data: []uint8{}}
		if len(pcm) >= minWindowBytes {
			out, err := r.emit(alignrt.Request{
				ID: fmt.Sprintf("%s-%d-%d", r.row.ID, fi, k), FileQuickHash: f.QuickHash, OffsetMs: reqStart, PCM: pcm,
			})
			if err != nil {
				return err
			}
			off := int((int64(k)*WindowMs - reqStart) / FrameMs)
			keep := int(min(int64(WindowFrames), (f.DurationMs+FrameMs-1)/FrameMs-int64(k)*WindowFrames))
			keep = max(0, min(keep, out.Frames-off))
			em = QuantiseMatrix(Matrix{T: keep, V: out.Vocab, LogP: out.LogProbs[off*out.Vocab : (off+keep)*out.Vocab]})
		}
		if err := r.s.cache.Write(f.QuickHash, r.row.ModelSHA256, k, em); err != nil {
			return err
		}
		r.emitted++
		r.winDone++
		r.prog.AudioDoneMs += windowMs(f, k)
		r.report()
		r.s.hook(r.ctx, "window", r.emitted)
		if err := r.stopped(); err != nil {
			return err
		}
	}
	return nil
}

// emit asks the worker for one window. A failure gets a fresh emitter and two more tries, then the job holds.
func (r *run) emit(req alignrt.Request) (alignrt.Emissions, error) {
	var last error
	for attempt := 0; attempt < emitAttempts; attempt++ {
		if err := r.stopped(); err != nil {
			return alignrt.Emissions{}, err
		}
		if r.em == nil {
			em, err := r.s.d.Runtimes.Emitter(r.ctx, r.lang)
			if errors.Is(err, alignrt.ErrNotUsable) {
				return alignrt.Emissions{}, r.hold(HoldRuntimeChanged, "The alignment model changed. Continue with the new model, or discard this.")
			}
			if err != nil {
				last = err
				continue
			}
			if em.Identity().ModelSHA256 != r.row.ModelSHA256 {
				return alignrt.Emissions{}, r.hold(HoldRuntimeChanged, "The alignment model changed. Continue with the new model, or discard this.")
			}
			r.em = em
		}
		out, err := r.em.Emit(r.ctx, req)
		if err == nil && (out.Vocab != r.vocab.Size() || out.FrameMs != FrameMs || out.Frames < 0 || len(out.LogProbs) != out.Frames*out.Vocab) {
			err = alignrt.ErrProtocol
		}
		if err == nil {
			return out, nil
		}
		last = err
		r.em = nil
	}
	if err := r.stopped(); err != nil {
		return alignrt.Emissions{}, err
	}
	r.s.d.Log.Warn("align: the alignment worker failed", "alignment", r.row.ID, "err", last)
	return alignrt.Emissions{}, r.hold(HoldWorkerFailed, "The alignment worker stopped on part of this book. Try again, or discard this.")
}

func (r *run) alignStage(doc *ebook.Doc) error {
	s, ctx := r.s, r.ctx
	if err := r.settle(StateAligning, ""); err != nil {
		return err
	}
	track := Track{}
	for _, f := range r.files {
		track.Files = append(track.Files, TrackFile{QuickHash: f.QuickHash, OffsetMs: f.OffsetMs, DurationMs: f.DurationMs})
	}
	text := BuildText(doc, r.lang, r.vocab)
	r.prog.Stage, r.prog.ChaptersTotal = StateAligning, len(text.Chapters)
	r.report()
	timings := s.timingDir()
	res, err := Align(Input{
		Text: text, Vocab: r.vocab, Src: newCacheSource(s.cache, track, r.row.ModelSHA256, r.vocab), Track: track,
		ModelSHA: r.row.ModelSHA256, Params: s.d.Params,
		Reuse: func(_ int, key string) (*Timing, bool) {
			if _, ok, err := s.store.ChapterByKey(ctx, key); err != nil || !ok {
				return nil, false
			}
			t, err := ReadTiming(timings, key)
			return t, err == nil
		},
		Stop: r.stopped,
		OnChapter: func(cr ChapterResult) error {
			if cr.Timing != nil && !cr.Reused {
				if err := WriteTiming(timings, cr.Key, cr.Timing); err != nil {
					return err
				}
			}
			ch := ChapterRow{Idx: cr.Idx, State: cr.State, ChapterKey: cr.Key, SentencesTotal: cr.Total, SentencesTimed: cr.Timed}
			if cr.Timed > 0 {
				start, end, conf := cr.StartMS, cr.EndMS, cr.MeanConfidence
				ch.StartMS, ch.EndMS, ch.MeanConfidence = &start, &end, &conf
			}
			ok, err := s.store.PutChapter(ctx, r.row.ID, r.row.JobID, ch)
			if err != nil {
				return err
			}
			if !ok {
				return errGone
			}
			r.prog.ChaptersDone++
			r.report()
			s.hook(ctx, "chapter", r.prog.ChaptersDone)
			return r.stopped()
		},
	})
	if err != nil {
		if cerr := r.stopped(); cerr != nil {
			return cerr
		}
		return err
	}
	s.hook(ctx, "viterbi", res.ViterbiRuns)
	ok, err := s.store.Finish(ctx, r.row.ID, r.row.JobID, Finish{
		State: res.State, Labels: res.Labels, Coverage: res.Coverage, SentencesTotal: res.Total, SentencesTimed: res.Timed,
		ErrorCode: res.Code,
	})
	if err != nil {
		return err
	}
	if !ok {
		return errGone
	}
	keep := map[string]bool{}
	for _, f := range r.files {
		keep[f.QuickHash+"/"+r.row.ModelSHA256] = true
	}
	if s.d.CacheMaxBytes > 0 {
		if _, err := s.cache.Prune(s.d.CacheMaxBytes, keep); err != nil {
			s.d.Log.Warn("align: pruning the emissions cache failed", "err", err)
		}
	}
	if res.State == StateFailed {
		return &jobs.Error{Code: res.Code, Text: "Tendshelf could not find this book's text in the recording.", Permanent: true}
	}
	return nil
}

// pcmStream reads one ffmpeg decode as a sequence of overlapping windows.
type pcmStream struct {
	pr     *io.PipeReader
	cancel context.CancelFunc
	done   chan struct{}
	buf    []byte
	base   int64 // stream byte offset of buf[0]
	eof    bool
}

func (s *Service) openStream(ctx context.Context, path string, fromMs, durMs int64) *pcmStream {
	cctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	st := &pcmStream{pr: pr, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(st.done)
		pw.CloseWithError(s.d.Decoder.PCM16k(cctx, path, fromMs, durMs, pw))
	}()
	return st
}

// window returns the bytes [from, to) of the stream, fewer when it ends sooner. Callers ask for
// non-decreasing from values; earlier bytes are dropped.
func (p *pcmStream) window(from, to int64) ([]byte, error) {
	if n := min(from-p.base, int64(len(p.buf))); n > 0 {
		p.buf, p.base = p.buf[n:], p.base+n
	}
	chunk := make([]byte, 64<<10)
	for !p.eof && p.base+int64(len(p.buf)) < to {
		n, err := p.pr.Read(chunk)
		p.buf = append(p.buf, chunk[:n]...)
		switch {
		case errors.Is(err, io.EOF):
			p.eof = true
		case err != nil:
			return nil, err
		}
	}
	lo := max(from-p.base, 0)
	hi := min(to-p.base, int64(len(p.buf)))
	if lo >= hi {
		return nil, nil
	}
	return append([]byte(nil), p.buf[lo:hi]...), nil
}

// close stops the decoder and waits for it.
func (p *pcmStream) close() {
	p.cancel()
	p.pr.Close()
	<-p.done
}
