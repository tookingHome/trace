package catalog

import (
	"fmt"
	"os/exec"
	"strings"
)

func OpenRegedit(keyPath string) error {
	if keyPath == "" {
		return fmt.Errorf("empty registry path")
	}
	// Escape single quotes for PowerShell single-quoted string.
	safe := strings.ReplaceAll(keyPath, "'", "''")
	// Set LastKey so regedit opens near the target.
	ps := fmt.Sprintf(
		`New-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Applets\Regedit' -Force | Out-Null; Set-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Applets\Regedit' -Name LastKey -Value '%s'; Start-Process regedit.exe`,
		safe,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	return cmd.Start()
}
