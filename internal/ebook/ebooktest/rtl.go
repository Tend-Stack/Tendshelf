package ebooktest

import "strings"

// Synthetic right-to-left sample sentences for tests: fixed word lists, no real text.
var (
	arabicWords = strings.Fields("كتاب الولد ذهب إلى المدرسة في الصباح ثم عاد البيت")
	hebrewWords = strings.Fields("הילד הלך אל בית הספר בבוקר ואז חזר הביתה")
)

// ArabicParas returns n deterministic Arabic paragraphs of four sentences each. Sentence i ends with a
// full stop, every third one with the Arabic question mark.
func ArabicParas(n int) []string { return rtlParas(n, arabicWords, "؟") }

// HebrewParas is ArabicParas in Hebrew; every third sentence ends with "?".
func HebrewParas(n int) []string { return rtlParas(n, hebrewWords, "?") }

func rtlParas(n int, words []string, q string) []string {
	out := make([]string, 0, n)
	k := 0
	for p := 0; p < n; p++ {
		var sb strings.Builder
		for s := 0; s < 4; s++ {
			if s > 0 {
				sb.WriteByte(' ')
			}
			for w := 0; w < 5; w++ {
				if w > 0 {
					sb.WriteByte(' ')
				}
				sb.WriteString(words[(k+w*3)%len(words)])
			}
			k++
			if k%3 == 0 {
				sb.WriteString(q)
			} else {
				sb.WriteByte('.')
			}
		}
		out = append(out, sb.String())
	}
	return out
}
