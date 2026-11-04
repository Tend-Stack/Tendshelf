package textmatch

import "strings"

// ISBN13 returns the ISBN-13 form of s: a valid ISBN-10 converted to its 978-prefixed ISBN-13, a valid ISBN-13
// unchanged, else "".
func ISBN13(s string) string {
	v := ISBN(s)
	switch len(v) {
	case 13:
		return v
	case 10:
		body := "978" + v[:9]
		sum := 0
		for i := 0; i < 12; i++ {
			d := int(body[i] - '0')
			if i%2 == 1 {
				d *= 3
			}
			sum += d
		}
		return body + string(rune('0'+(10-sum%10)%10))
	}
	return ""
}

// Surname returns the comparable family name of one person's name. "Last, First" with exactly one comma uses
// the part before the comma; otherwise it is the last word, skipping trailing generation suffixes.
func Surname(name string) string {
	if strings.Count(name, ",") == 1 {
		if n := strings.Fields(Norm(name[:strings.Index(name, ",")])); len(n) > 0 {
			return strings.Join(n, " ")
		}
	}
	words := strings.Fields(Norm(name))
	for len(words) > 1 {
		switch words[len(words)-1] {
		case "jr", "sr", "ii", "iii", "iv":
			words = words[:len(words)-1]
			continue
		}
		break
	}
	if len(words) == 0 {
		return ""
	}
	return words[len(words)-1]
}

// TitleKey reduces a title to what two editions of one book share: no trailing "(Series #1)" or "[Unabridged]"
// groups (unless that would empty it), no subtitle after ": ", no trailing "a novel", no "unabridged"/"abridged".
func TitleKey(title string) string {
	t := strings.TrimSpace(title)
	for {
		open := ""
		switch {
		case strings.HasSuffix(t, ")"):
			open = "("
		case strings.HasSuffix(t, "]"):
			open = "["
		}
		if open == "" {
			break
		}
		i := strings.LastIndex(t, open)
		if i <= 0 || strings.TrimSpace(t[:i]) == "" {
			break
		}
		t = strings.TrimSpace(t[:i])
	}
	if i := strings.Index(t, ": "); i > 0 && strings.TrimSpace(t[:i]) != "" {
		t = t[:i]
	}
	var out []string
	for _, w := range strings.Fields(Norm(t)) {
		if w == "unabridged" || w == "abridged" {
			continue
		}
		out = append(out, w)
	}
	if n := len(out); n > 2 && out[n-2] == "a" && out[n-1] == "novel" {
		out = out[:n-2]
	}
	return strings.Join(out, " ")
}
