package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type InstallSource string

const (
	SourceRegistry InstallSource = "Registry"
)

type Program struct {
	ID                string
	DisplayName       string
	Publisher         string
	Version           string
	InstallDate       string
	EstimatedBytes    int64
	InstallLocation   string
	DisplayIcon      string
	UninstallString   string
	QuietUninstall    string
	IsSystemComponent bool
	Source            InstallSource
	RegistryKeyPath   string
}

func (p Program) SizeDisplay() string {
	if p.EstimatedBytes <= 0 {
		return "—"
	}
	mb := float64(p.EstimatedBytes) / (1024.0 * 1024.0)
	if mb < 1024 {
		return fmt.Sprintf("%.1f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", mb/1024.0)
}

func Load(includeSystem bool) ([]Program, error) {
	byID := map[string]Program{}

	roots := []struct {
		hive registry.Key
		path string
		name string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, "HKEY_LOCAL_MACHINE"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, "HKEY_LOCAL_MACHINE"},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, "HKEY_CURRENT_USER"},
	}

	for _, r := range roots {
		if err := readHive(r.hive, r.path, r.name, byID); err != nil {
			// Continue other hives even if one fails.
			continue
		}
	}

	out := make([]Program, 0, len(byID))
	for _, p := range byID {
		if !includeSystem && p.IsSystemComponent {
			continue
		}
		if strings.TrimSpace(p.DisplayName) == "" || strings.TrimSpace(p.UninstallString) == "" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out, nil
}

func readHive(hive registry.Key, rootPath, hiveName string, byID map[string]Program) error {
	k, err := registry.OpenKey(hive, rootPath, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		sk, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, ok := parseKey(sk, hiveName+`\`+rootPath+`\`+name)
		sk.Close()
		if !ok {
			continue
		}
		if existing, exists := byID[p.ID]; !exists || richer(p, existing) {
			byID[p.ID] = p
		}
	}
	return nil
}

func parseKey(k registry.Key, fullPath string) (Program, bool) {
	display, _, err := k.GetStringValue("DisplayName")
	if err != nil || strings.TrimSpace(display) == "" {
		return Program{}, false
	}
	if release, _, err := k.GetStringValue("ReleaseType"); err == nil &&
		strings.Contains(strings.ToLower(release), "update") {
		return Program{}, false
	}

	uninstall, _, _ := k.GetStringValue("UninstallString")
	quiet, _, _ := k.GetStringValue("QuietUninstallString")
	publisher, _, _ := k.GetStringValue("Publisher")
	version, _, _ := k.GetStringValue("DisplayVersion")
	location, _, _ := k.GetStringValue("InstallLocation")
	icon, _, _ := k.GetStringValue("DisplayIcon")
	installDate, _, _ := k.GetStringValue("InstallDate")
	sys, _, _ := k.GetIntegerValue("SystemComponent")

	var sizeBytes int64
	if kib, _, err := k.GetIntegerValue("EstimatedSize"); err == nil && kib > 0 {
		// Registry EstimatedSize is kilobytes.
		sizeBytes = int64(kib) * 1024
	}

	location = cleanPath(location)
	p := Program{
		ID:                buildID(display, publisher, location, uninstall),
		DisplayName:       strings.TrimSpace(display),
		Publisher:         strings.TrimSpace(publisher),
		Version:           strings.TrimSpace(version),
		InstallDate:       normalizeDate(installDate),
		EstimatedBytes:    sizeBytes,
		InstallLocation:   location,
		DisplayIcon:      strings.TrimSpace(icon),
		UninstallString:   strings.TrimSpace(uninstall),
		QuietUninstall:    strings.TrimSpace(quiet),
		IsSystemComponent: sys == 1,
		Source:            SourceRegistry,
		RegistryKeyPath:   fullPath,
	}
	return p, true
}

func richer(candidate, existing Program) bool {
	score := 0
	if candidate.InstallLocation != "" {
		score += 2
	}
	if candidate.EstimatedBytes > 0 {
		score++
	}
	if candidate.Version != "" {
		score++
	}
	existingScore := 0
	if existing.InstallLocation != "" {
		existingScore += 2
	}
	if existing.EstimatedBytes > 0 {
		existingScore++
	}
	if existing.Version != "" {
		existingScore++
	}
	return score >= existingScore
}

func buildID(display, publisher, location, uninstall string) string {
	raw := strings.ToLower(display + "|" + publisher + "|" + location + "|" + uninstall)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}

func normalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) == 8 {
		if _, err := strconv.Atoi(v); err == nil {
			return v[0:4] + "-" + v[4:6] + "-" + v[6:8]
		}
	}
	return v
}

func BaseName(path string) string {
	return filepath.Base(path)
}
