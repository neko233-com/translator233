// Package lang provides language codes, display names and heuristic detection.
package lang

import (
	"strings"
	"unicode"
)

// Name returns the Chinese display name of a language code.
func Name(code string) string {
	if n, ok := names[strings.ToLower(code)]; ok {
		return n
	}
	return code
}

var names = map[string]string{
	"zh": "中文", "en": "英语", "ja": "日语", "ko": "韩语", "fr": "法语",
	"de": "德语", "es": "西班牙语", "ru": "俄语", "ar": "阿拉伯语", "it": "意大利语",
	"vi": "越南语", "th": "泰语", "id": "印尼语", "tr": "土耳其语", "nl": "荷兰语",
	"pl": "波兰语", "uk": "乌克兰语", "hi": "印地语", "sv": "瑞典语", "cs": "捷克语",
	"fi": "芬兰语", "da": "丹麦语", "pt": "葡萄牙语", "he": "希伯来语", "el": "希腊语",
	"ms": "马来语", "ro": "罗马尼亚语", "fa": "波斯语",
}

// Words holds per-language function words used by the Latin-script detector.
var Words = map[string][]string{
	"en": {"the", "and", "is", "to", "of", "you", "that", "it", "are", "was", "for"},
	"fr": {"le", "la", "les", "et", "est", "des", "une", "que", "pour", "dans", "vous"},
	"de": {"der", "die", "das", "und", "ist", "nicht", "sie", "mit", "auf", "ein", "für"},
	"es": {"el", "la", "los", "y", "es", "que", "de", "una", "para", "con", "por"},
	"it": {"il", "la", "gli", "e", "che", "di", "una", "per", "con", "non", "sono"},
	"pt": {"o", "a", "os", "e", "que", "de", "uma", "para", "com", "não", "você"},
	"nl": {"de", "het", "een", "en", "van", "niet", "dat", "op", "voor", "zijn"},
	"vi": {"và", "là", "của", "không", "có", "được", "cho", "một", "này", "những"},
	"tr": {"ve", "bir", "bu", "için", "ile", "değil", "olarak", "çok", "daha"},
	"id": {"dan", "yang", "untuk", "tidak", "dengan", "ini", "itu", "dari", "akan"},
	"pl": {"i", "w", "nie", "na", "jest", "się", "że", "do", "dla", "to"},
	"sv": {"och", "att", "det", "är", "som", "för", "med", "inte", "den"},
	"da": {"og", "at", "det", "er", "som", "for", "med", "ikke", "den"},
	"cs": {"a", "v", "se", "na", "je", "že", "do", "pro", "to", "není"},
	"fi": {"ja", "on", "ei", "että", "se", "kun", "niin", "oli", "voi"},
}

// Detect guesses the language of text. It returns "en" for unclassifiable Latin
// text and "" when no guess is possible.
func Detect(text string) string {
	var han, kana, hangul, cyrillic, arabic, thai, devanagari, greek, hebrew, latin int
	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.Is(unicode.Arabic, r):
			arabic++
		case unicode.Is(unicode.Thai, r):
			thai++
		case unicode.Is(unicode.Devanagari, r):
			devanagari++
		case unicode.Is(unicode.Greek, r):
			greek++
		case unicode.Is(unicode.Hebrew, r):
			hebrew++
		case unicode.IsLetter(r):
			latin++
		}
	}
	switch {
	case kana > 0:
		return "ja"
	case hangul > 0:
		return "ko"
	case han > 0:
		return "zh"
	case cyrillic > han && cyrillic > latin:
		return "ru"
	case arabic > latin:
		return "ar"
	case thai > latin:
		return "th"
	case devanagari > latin:
		return "hi"
	case greek > latin:
		return "el"
	case hebrew > latin:
		return "he"
	case latin > 0:
		return detectLatin(text)
	}
	return ""
}

func detectLatin(text string) string {
	lower := " " + strings.ToLower(text) + " "
	fields := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	seen := make(map[string]bool, len(fields))
	for _, w := range fields {
		seen[w] = true
	}
	best, bestScore := "en", 0
	for code, words := range Words {
		score := 0
		for _, w := range words {
			if seen[w] {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = code, score
		}
	}
	return best
}
