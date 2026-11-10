package podcast

import (
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// Plain-words texts the jobs and handlers show (BRAND voice). No upstream address, header or error
// text ever goes into one.
const (
	TextOff         = "Podcasts are turned off on this server."
	TextShowGone    = "That show is not on this server any more."
	TextEpisodeGone = "That episode is not on this server any more."
	TextBadAddress  = "That is not a web address Tendshelf can follow. Paste the show's RSS address."
	TextTooMany     = "You follow 200 shows, the most Tendshelf keeps."
	TextTooSoon     = "This show was just refreshed. Try again in a few minutes."
	TextVideo       = "This episode is a video, which Tendshelf doesn't play."
	TextNotFeed     = "That address isn't a podcast feed."
	TextBusy        = "Too many podcasts at once. Wait a minute and try again."
	TextCacheOff    = "This server keeps no saved episodes."
)

// FailText explains a stored fail_code in plain words ("" when there is no failure).
func FailText(code string) string {
	switch code {
	case "":
		return ""
	case "refused":
		return "Tendshelf can't fetch from that address. It must be a public web address."
	case "offline":
		return "Tendshelf couldn't reach this show's website. It will try again later."
	case "not_found":
		return "This show's feed is gone from its address."
	case "not_a_feed":
		return "That address isn't a podcast feed."
	case "too_large":
		return "This show's feed is too large to read."
	case "slow":
		return "This show's website is too slow to answer. It will try again later."
	case "busy":
		return "This show's website asked Tendshelf to wait. It will try again later."
	}
	return "This show's feed couldn't be read."
}

// netCode is the stable code of a podnet failure, or "bad_answer" for anything unexpected.
func netCode(err error) string {
	var pe *podnet.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return "bad_answer"
}
