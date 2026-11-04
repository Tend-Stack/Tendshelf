package ebook

// Refusal reasons, stored in ebook_files.refused_reason.
const (
	ReasonDRM         = "drm"
	ReasonUnsupported = "unsupported"
	ReasonDamaged     = "damaged"
	ReasonTooLarge    = "too_large"
	ReasonNoText      = "no_text"
)

// Refusal is the error of a file Tendshelf will not read: a reason for the owner and a short code for
// the log. Details never include file contents or paths.
type Refusal struct {
	Reason string
	Code   string
}

func (r *Refusal) Error() string { return "ebook refused: " + r.Reason + " (" + r.Code + ")" }

func refuse(reason, code string) *Refusal { return &Refusal{Reason: reason, Code: code} }

func damaged(code string) *Refusal { return refuse(ReasonDamaged, code) }
