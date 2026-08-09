package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ScanConfidence int

const (
	ConfidenceHigh ScanConfidence = iota
	ConfidenceMedium
	ConfidenceLow
)

func (c ScanConfidence) String() string {
	switch c {
	case ConfidenceHigh:
		return "高"
	case ConfidenceMedium:
		return "中"
	default:
		return "低"
	}
}

type ScanHitKind int

const (
	HitFolder ScanHitKind = iota
	HitFile
	HitRegistryKey
)

func (k ScanHitKind) String() string {
	switch k {
	case HitFolder:
		return "文件夹"
	case HitFile:
		return "文件"
	case HitRegistryKey:
		return "注册表"
	default:
		return "项"
	}
}

type ScanHit struct {
	Kind       ScanHitKind
	Path       string
	Confidence ScanConfidence
	Reason     string
	SizeBytes  int64
	Protected  bool
}

func (h ScanHit) DefaultSelected() bool {
	return !h.Protected
}

func (h ScanHit) SizeLabel() string {
	if h.Kind == HitRegistryKey || h.SizeBytes <= 0 {
		return "—"
	}
	mb := float64(h.SizeBytes) / (1024 * 1024)
	if mb < 0.1 {
		return fmt.Sprintf("%d KB", h.SizeBytes/1024)
	}
	if mb < 1024 {
		return fmt.Sprintf("%.1f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", mb/1024)
}

// ScanLeftovers finds high-certainty leftovers after uninstall.
// Only exact / install-anchored hits are returned (no medium/low guesses).
func ScanLeftovers(p Program) []ScanHit {
	var hits []ScanHit
	display := strings.TrimSpace(p.DisplayName)

	if loc := cleanPath(p.InstallLocation); loc != "" && loc != "." {
		if st, err := os.Stat(loc); err == nil && st.IsDir() {
			hits = append(hits, folderHit(loc, ConfidenceHigh, "安装目录仍存在"))
		}
	}

	for _, root := range leftoverRoots() {
		hits = append(hits, matchExactChildren(root, display)...)
	}
	hits = append(hits, matchExactChildrenInTemps(display)...)
	hits = append(hits, scanRegistryLeftovers(p)...)

	out := make([]ScanHit, 0, len(hits))
	for _, h := range dedupeHits(hits) {
		if h.Confidence == ConfidenceHigh {
			out = append(out, h)
		}
	}
	return out
}

func leftoverRoots() []string {
	home, _ := os.UserHomeDir()
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	pd := os.Getenv("ProgramData")
	pf := os.Getenv("ProgramFiles")
	pf86 := os.Getenv("ProgramFiles(x86)")
	roots := []string{pf, pf86, pd, local, roaming}
	if home != "" {
		roots = append(roots,
			filepath.Join(home, "Desktop"),
			filepath.Join(home, "AppData", "Local"),
			filepath.Join(home, "AppData", "Roaming"),
		)
	}
	if local != "" {
		roots = append(roots, filepath.Join(local, "Programs"))
	}
	// Start Menu
	if roaming != "" {
		roots = append(roots, filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	if pd != "" {
		roots = append(roots, filepath.Join(pd, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	return uniqueNonEmpty(roots)
}

func scanTempDirs(tokens []string) []ScanHit {
	return nil
}

func matchExactChildrenInTemps(displayName string) []ScanHit {
	temps := uniqueNonEmpty([]string{
		os.TempDir(),
		os.Getenv("TEMP"),
		os.Getenv("TMP"),
	})
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		temps = append(temps, filepath.Join(local, "Temp"))
	}
	var hits []ScanHit
	for _, root := range uniqueNonEmpty(temps) {
		hits = append(hits, matchExactChildren(root, displayName)...)
	}
	return hits
}

func matchExactChildren(root, displayName string) []ScanHit {
	root = strings.TrimSpace(root)
	displayName = strings.TrimSpace(displayName)
	if root == "" || displayName == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var hits []ScanHit
	for _, e := range entries {
		name := e.Name()
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if e.IsDir() {
			if !strings.EqualFold(name, displayName) {
				continue
			}
			full := filepath.Join(root, name)
			hits = append(hits, folderHit(full, ConfidenceHigh, "残留文件夹"))
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".lnk" && ext != ".url" {
			continue
		}
		if !strings.EqualFold(base, displayName) {
			continue
		}
		full := filepath.Join(root, name)
		hits = append(hits, ScanHit{
			Kind:       HitFile,
			Path:       full,
			Confidence: ConfidenceHigh,
			Reason:     "残留快捷方式",
			Protected:  isProtectedPath(full),
		})
	}
	return hits
}

func matchChildDirs(root, displayName string, tokens []string) []ScanHit {
	return matchExactChildren(root, displayName)
}

func folderHit(path string, conf ScanConfidence, reason string) ScanHit {
	return ScanHit{
		Kind:       HitFolder,
		Path:       path,
		Confidence: conf,
		Reason:     reason,
		SizeBytes:  dirSizeSafe(path),
		Protected:  isProtectedPath(path),
	}
}

func buildTokens(p Program) []string {
	set := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 3 {
			return
		}
		set[s] = struct{}{}
		for _, part := range strings.FieldsFunc(s, func(r rune) bool {
			return r == ' ' || r == '-' || r == '_' || r == '.' || r == ',' ||
				r == '(' || r == ')' || r == '[' || r == ']' || r == ';' || r == '+'
		}) {
			part = strings.Trim(part, ".,;:+")
			if len(part) >= 4 && !isNoiseToken(part) {
				set[part] = struct{}{}
			}
		}
	}
	add(p.DisplayName)
	add(p.Publisher)
	if loc := cleanPath(p.InstallLocation); loc != "" && loc != "." {
		add(filepath.Base(loc))
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

func isNoiseToken(s string) bool {
	switch strings.ToLower(s) {
	case "inc", "ltd", "llc", "corp", "corporation", "software", "app", "application",
		"windows", "microsoft", "the", "for", "and", "version", "setup", "install",
		"com", "org", "net", "folder", "folders", "file", "files", "data", "free",
		"pro", "plus", "desktop", "home", "user", "temp", "cache", "program", "programs",
		"tool", "tools", "manager", "update", "updater", "service", "driver", "system":
		return true
	}
	return false
}

// matchedToken reports whether name likely belongs to the product.
// Long / multi-word tokens may match as substrings; short tokens must match a
// whole path segment to avoid hits like Folder ⊂ PlaceholderTileLogoFolder.
func matchedToken(name string, tokens []string) bool {
	lower := strings.ToLower(name)
	parts := splitPathSegments(name)
	for _, t := range tokens {
		tl := strings.ToLower(strings.TrimSpace(t))
		if tl == "" || isNoiseToken(tl) {
			continue
		}
		if strings.Contains(tl, " ") || len(tl) >= 8 {
			if strings.Contains(lower, tl) {
				return true
			}
			continue
		}
		for _, p := range parts {
			if p == tl {
				return true
			}
		}
	}
	return false
}

func splitPathSegments(name string) []string {
	name = strings.ToLower(filepath.Base(name))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == ',' ||
			r == '(' || r == ')' || r == '[' || r == ']'
	})
	// Also split CamelCase / PascalCase boundaries lightly: insert break before
	// runs we already handle via separators; keep whole token as well.
	out := make([]string, 0, len(parts)+1)
	if name != "" {
		out = append(out, name)
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isProtectedPath(path string) bool {
	path = cleanPath(path)
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	protected := []string{
		win,
		filepath.Join(win, "System32"),
		filepath.Join(win, "SysWOW64"),
		os.Getenv("SystemRoot"),
		os.Getenv("SystemDrive") + `\`,
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramData"),
		os.Getenv("PUBLIC"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		protected = append(protected, home)
	}
	for _, p := range protected {
		p = cleanPath(p)
		if p == "" || p == "." {
			continue
		}
		if strings.EqualFold(path, p) {
			return true
		}
		// Block deleting anything directly under Windows\
		if strings.EqualFold(filepath.Dir(path), cleanPath(win)) {
			return true
		}
	}
	return false
}

func dirSizeSafe(path string) int64 {
	var total int64
	n := 0
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		n++
		if n > 8000 {
			// Abort whole walk (SkipDir on a file does nothing and can run forever).
			return filepath.SkipAll
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func dedupeHits(hits []ScanHit) []ScanHit {
	best := map[string]ScanHit{}
	order := make([]string, 0, len(hits))
	for _, h := range hits {
		key := strings.ToLower(h.Path)
		if old, ok := best[key]; ok {
			if h.Confidence < old.Confidence {
				best[key] = h
			}
			continue
		}
		best[key] = h
		order = append(order, key)
	}
	out := make([]ScanHit, 0, len(best))
	for _, k := range order {
		out = append(out, best[k])
	}
	// High first
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Confidence < out[i].Confidence {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(s))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
