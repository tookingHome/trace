package catalog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func runDetached(file string, args ...string) error {
	cmd := exec.Command(file, args...)
	// Hide the helper console only; child GUI apps still show.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd.Start()
}

func OpenFolder(path string) error {
	path = cleanPath(path)
	if path == "" {
		return fmt.Errorf("empty path")
	}

	// Prefer selecting a file when path points to an exe/icon.
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		cmd := exec.Command("explorer.exe", "/select,"+path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
		if err := cmd.Start(); err == nil {
			return nil
		}
	}

	// 1) ShellExecute the folder itself (shows Explorer window).
	if err := shellExecute("explore", path, "", ""); err == nil {
		return nil
	}
	if err := shellExecute("open", path, "", ""); err == nil {
		return nil
	}

	// 2) explorer.exe without HideWindow (previous HideWindow hid the window).
	cmd := exec.Command("explorer.exe", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	if err := cmd.Start(); err == nil {
		return nil
	}

	// 3) cmd start
	cmd = exec.Command("cmd.exe", "/C", "start", "", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Start()
}

func shellExecute(verb, file, args, cwd string) error {
	v, err := syscallUTF16(verb)
	if err != nil {
		return err
	}
	f, err := syscallUTF16(file)
	if err != nil {
		return err
	}
	var a, d *uint16
	if args != "" {
		a, err = syscallUTF16(args)
		if err != nil {
			return err
		}
	}
	if cwd != "" {
		d, err = syscallUTF16(cwd)
		if err != nil {
			return err
		}
	}
	err = windows.ShellExecute(0, v, f, a, d, windows.SW_SHOWNORMAL)
	if err != nil {
		return err
	}
	return nil
}

func syscallUTF16(s string) (*uint16, error) {
	if s == "" {
		return nil, nil
	}
	return windows.UTF16PtrFromString(s)
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"`)
	path = os.ExpandEnv(path)
	// DisplayIcon often has ",0" resource suffix.
	if i := strings.LastIndex(path, ","); i > 2 {
		suffix := path[i+1:]
		if isIntLike(suffix) {
			path = path[:i]
		}
	}
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"`)
	return filepath.Clean(path)
}

func isIntLike(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c != '-' {
				return false
			}
		}
	}
	return true
}

// ResolveInstallDir finds a browsable folder for a program.
func ResolveInstallDir(p Program) string {
	candidates := []string{
		p.InstallLocation,
		dirOf(p.DisplayIcon),
		dirOfCommand(p.UninstallString),
		dirOfCommand(p.QuietUninstall),
	}
	for _, c := range candidates {
		if dir := existingDir(c); dir != "" {
			return dir
		}
	}
	// Last resort: return InstallLocation even if missing on disk (Explorer may still help).
	if loc := cleanPath(p.InstallLocation); loc != "" && loc != "." {
		return loc
	}
	return ""
}

func existingDir(path string) string {
	path = cleanPath(path)
	if path == "" || path == "." {
		return ""
	}
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			return path
		}
		return filepath.Dir(path)
	}
	// Parent of a path that looks absolute.
	if filepath.IsAbs(path) {
		parent := filepath.Dir(path)
		if st, err := os.Stat(parent); err == nil && st.IsDir() {
			// Avoid returning drive root from msiexec-style junk unless it was the install loc.
			if len(parent) > 3 {
				return parent
			}
		}
	}
	return ""
}

func dirOf(path string) string {
	path = cleanPath(path)
	if path == "" || path == "." {
		return ""
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return path
	}
	return filepath.Dir(path)
}

func dirOfCommand(command string) string {
	file, _, err := splitCommand(command)
	if err != nil || file == "" {
		return ""
	}
	file = cleanPath(file)
	base := strings.ToLower(filepath.Base(file))
	// msiexec / setup bootstrapper paths are not install dirs.
	if strings.HasPrefix(base, "msiexec") || base == "cmd.exe" || base == "powershell.exe" {
		return ""
	}
	return dirOf(file)
}

// isPersistentHostExe reports executables that often stay running after handing
// an uninstall request to an existing instance (Chrome PWAs, Steam games, etc.).
// Waiting on these processes freezes TRACE until the user quits the host app.
func isPersistentHostExe(file string) bool {
	base := strings.ToLower(filepath.Base(cleanPath(file)))
	switch base {
	case "chrome.exe", "msedge.exe", "msedgewebview2.exe",
		"brave.exe", "firefox.exe", "opera.exe", "vivaldi.exe",
		"steam.exe", "epicgameslauncher.exe",
		"explorer.exe":
		return true
	default:
		return false
	}
}

// isPersistentHostUninstall reports uninstall commands that must not be Wait()'d:
// browser/Steam hosts and protocol / app-id style hand-offs.
func isPersistentHostUninstall(file, params string) bool {
	if isPersistentHostExe(file) {
		return true
	}
	pl := strings.ToLower(strings.TrimSpace(params))
	if pl == "" {
		return false
	}
	// Protocol / PWA style: host process usually keeps running.
	if strings.Contains(pl, "steam://") || strings.Contains(pl, "uninstall-app-id") {
		return true
	}
	return false
}

// LaunchUninstall runs the program's UninstallString and shows its UI.
// If wait is true, blocks until the uninstaller process exits — except for
// persistent host apps, where waiting would hang forever.
func LaunchUninstall(command string, wait bool) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty uninstall command")
	}
	file, params, err := parseUninstallCommand(command)
	if err != nil {
		return err
	}
	file = cleanPath(file)
	if file == "" {
		return fmt.Errorf("bad uninstall command")
	}
	// Prefer full path when registry only stores "MsiExec.exe".
	if resolved, err := exec.LookPath(file); err == nil {
		file = resolved
	}

	cwd := ""
	if filepath.IsAbs(file) {
		cwd = filepath.Dir(file)
	}

	host := isPersistentHostUninstall(file, params)
	// Hosts: prefer ShellExecute and never Wait on the process.
	if host {
		if err := shellExecute("open", file, params, cwd); err != nil {
			cmd := exec.Command(file, splitArgs(params)...)
			cmd.Dir = cwd
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
			if err2 := cmd.Start(); err2 != nil {
				return err
			}
		}
		return nil
	}

	args := splitArgs(params)
	cmd := exec.Command(file, args...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	if err := cmd.Start(); err != nil {
		// ShellExecute fallback (e.g. msiexec aliases).
		if err2 := shellExecute("open", file, params, cwd); err2 != nil {
			return err
		}
		return nil
	}
	if wait {
		_ = cmd.Wait()
	}
	return nil
}

// WaitUninstallAndScan starts uninstall, waits for completion signal, then scans.
func WaitUninstallAndScan(p Program) ([]ScanHit, error) {
	file, params, _ := parseUninstallCommand(p.UninstallString)
	host := isPersistentHostUninstall(file, params)

	if err := LaunchUninstall(p.UninstallString, !host); err != nil {
		return nil, err
	}

	// Host apps: only registry disappearance proves completion.
	// Normal uninstallers: process already exited; poll briefly for delayed key removal.
	maxWait := 45 * time.Second
	if host {
		maxWait = 90 * time.Second
	}
	gone := waitUninstallKeyGone(p.RegistryKeyPath, maxWait)
	// If the uninstall entry is still present, the product is likely still installed.
	// Do NOT scan/offer deleting InstallLocation — that would wipe a live app.
	if p.RegistryKeyPath != "" && !gone {
		return nil, fmt.Errorf("卸载未完成或已取消")
	}
	time.Sleep(800 * time.Millisecond)
	return ScanLeftovers(p), nil
}

func waitUninstallKeyGone(keyPath string, maxWait time.Duration) bool {
	if strings.TrimSpace(keyPath) == "" {
		time.Sleep(2 * time.Second)
		return true
	}
	if !registryKeyExists(keyPath) {
		return true
	}
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if !registryKeyExists(keyPath) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return !registryKeyExists(keyPath)
}

func splitArgs(params string) []string {
	params = strings.TrimSpace(params)
	if params == "" {
		return nil
	}
	// Lightweight split: keep quoted segments intact.
	var out []string
	var b strings.Builder
	inQ := false
	for _, r := range params {
		switch {
		case r == '"':
			inQ = !inQ
		case r == ' ' && !inQ:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func parseUninstallCommand(command string) (string, string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", fmt.Errorf("empty uninstall command")
	}
	if strings.HasPrefix(command, `"`) {
		end := strings.Index(command[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("bad quoted command")
		}
		file := command[1 : 1+end]
		rest := strings.TrimSpace(command[2+end:])
		return file, rest, nil
	}

	// Unquoted paths often contain spaces, e.g.
	// D:\sun login client\SunloginClient\SunloginClient.exe --mod=uninstall
	if file, rest, ok := splitUnquotedExe(command); ok {
		return file, rest, nil
	}

	parts := strings.SplitN(command, " ", 2)
	if len(parts) == 1 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}

func splitUnquotedExe(command string) (string, string, bool) {
	lower := strings.ToLower(command)
	exts := []string{".exe", ".bat", ".cmd", ".com", ".msi"}
	type cand struct{ end int }
	var existing, first *cand
	for _, ext := range exts {
		search := 0
		for {
			i := strings.Index(lower[search:], ext)
			if i < 0 {
				break
			}
			end := search + i + len(ext)
			if end < len(command) {
				switch command[end] {
				case ' ', '\t', '"':
					// args follow
				default:
					// e.g. ".executable" — keep scanning
					search = end
					continue
				}
			}
			c := cand{end: end}
			if first == nil {
				first = &c
			}
			if _, err := os.Stat(cleanPath(command[:end])); err == nil {
				existing = &c
				break
			}
			search = end
		}
		if existing != nil {
			break
		}
	}
	pick := existing
	if pick == nil {
		pick = first
	}
	if pick == nil {
		return accumulateExistingPath(command)
	}
	file := command[:pick.end]
	rest := strings.TrimSpace(command[pick.end:])
	return file, rest, true
}

// accumulateExistingPath joins space-separated tokens until an on-disk file is found.
func accumulateExistingPath(command string) (string, string, bool) {
	tokens := splitArgs(command)
	if len(tokens) == 0 {
		return "", "", false
	}
	var b strings.Builder
	for i, tok := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(tok)
		candidate := b.String()
		if _, err := os.Stat(cleanPath(candidate)); err == nil {
			rest := strings.TrimSpace(strings.Join(tokens[i+1:], " "))
			return candidate, rest, true
		}
	}
	return "", "", false
}

func splitCommand(command string) (string, []string, error) {
	file, params, err := parseUninstallCommand(command)
	if err != nil {
		return "", nil, err
	}
	if params == "" {
		return file, nil, nil
	}
	return file, []string{params}, nil
}
