package chromeapi

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

func desktopChromeUA(major string) string {
	if strings.TrimSpace(major) == "" {
		major = "151"
	}
	return fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", major)
}

func chromeMajor(path string) string {
	if strings.TrimSpace(path) == "" {
		return "151"
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "151"
	}
	match := regexp.MustCompile(`(?:Chrome|Chromium)[/\s]+(\d+)`).FindStringSubmatch(string(out))
	if len(match) == 2 {
		return match[1]
	}
	return "151"
}
