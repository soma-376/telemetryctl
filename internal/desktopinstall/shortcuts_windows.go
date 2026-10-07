package desktopinstall

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func guiShortcuts() ([]string, error) {
	var names []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_Programs} {
		dir, err := windows.KnownFolderPath(id, 0)
		if err != nil {
			return nil, err
		}
		names = append(names, filepath.Join(dir, "Pulsemetry.lnk"))
	}
	return names, nil
}
