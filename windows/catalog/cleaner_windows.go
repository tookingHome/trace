//go:build windows

package catalog

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func scanRegistryLeftovers(p Program) []ScanHit {
	var hits []ScanHit
	if p.RegistryKeyPath != "" && registryKeyExists(p.RegistryKeyPath) {
		hits = append(hits, ScanHit{
			Kind:       HitRegistryKey,
			Path:       p.RegistryKeyPath,
			Confidence: ConfidenceHigh,
			Reason:     "卸载项注册表仍存在",
			Protected:  false,
		})
	}
	return hits
}

func registryKeyExists(path string) bool {
	hive, sub, err := parseRegPath(path)
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(hive, sub, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

func parseRegPath(path string) (registry.Key, string, error) {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, `/`, `\`)
	upper := strings.ToUpper(path)
	var hive registry.Key
	var rest string
	switch {
	case strings.HasPrefix(upper, `HKEY_CURRENT_USER\`):
		hive, rest = registry.CURRENT_USER, path[len(`HKEY_CURRENT_USER\`):]
	case strings.HasPrefix(upper, `HKCU\`):
		hive, rest = registry.CURRENT_USER, path[len(`HKCU\`):]
	case strings.HasPrefix(upper, `HKEY_LOCAL_MACHINE\`):
		hive, rest = registry.LOCAL_MACHINE, path[len(`HKEY_LOCAL_MACHINE\`):]
	case strings.HasPrefix(upper, `HKLM\`):
		hive, rest = registry.LOCAL_MACHINE, path[len(`HKLM\`):]
	default:
		return 0, "", fmt.Errorf("unsupported registry path: %s", path)
	}
	return hive, rest, nil
}

// DeleteHit removes a leftover after user confirmation.
func DeleteHit(h ScanHit) error {
	if h.Protected || isProtectedPath(h.Path) {
		return fmt.Errorf("受保护路径，已跳过: %s", h.Path)
	}
	switch h.Kind {
	case HitFolder, HitFile:
		return os.RemoveAll(h.Path)
	case HitRegistryKey:
		return deleteRegistryKeyRecursive(h.Path)
	default:
		return fmt.Errorf("unknown hit kind")
	}
}

func deleteRegistryKeyRecursive(path string) error {
	hive, sub, err := parseRegPath(path)
	if err != nil {
		return err
	}
	return deleteSubKeyRecursive(hive, sub)
}

func deleteSubKeyRecursive(parent registry.Key, path string) error {
	k, err := registry.OpenKey(parent, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	names, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, name := range names {
		if err := deleteSubKeyRecursive(parent, path+`\`+name); err != nil {
			return err
		}
	}
	return registry.DeleteKey(parent, path)
}
