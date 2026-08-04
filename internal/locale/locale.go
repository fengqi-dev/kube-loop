package locale

import (
	"os"
	"strings"
)

// Language is a BCP 47-ish app language tag.
type Language string

const (
	English Language = "en"
	Chinese Language = "zh-CN"
)

// Preferred returns the OS UI language for native dialogs.
// It checks LANG / LC_ALL, then the platform UI language (Windows).
func Preferred() Language {
	if isChineseEnv() || isChineseUI() {
		return Chinese
	}
	return English
}

// IsChinese reports whether Preferred is Chinese.
func IsChinese() bool {
	return Preferred() == Chinese
}

func isChineseEnv() bool {
	lang := strings.ToLower(os.Getenv("LANG"))
	if lang == "" {
		lang = strings.ToLower(os.Getenv("LC_ALL"))
	}
	return strings.HasPrefix(lang, "zh")
}
