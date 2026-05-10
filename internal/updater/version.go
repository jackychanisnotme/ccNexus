package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares two semantic versions
// Returns: 1 if v1 > v2, -1 if v1 < v2, 0 if equal
func CompareVersions(v1, v2 string) (int, error) {
	v1 = normalizeSemanticVersion(v1)
	v2 = normalizeSemanticVersion(v2)

	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	if len(parts1) != 3 || len(parts2) != 3 {
		return 0, fmt.Errorf("invalid version format")
	}

	for i := 0; i < 3; i++ {
		n1, err := strconv.Atoi(parts1[i])
		if err != nil {
			return 0, fmt.Errorf("invalid version number: %s", parts1[i])
		}
		n2, err := strconv.Atoi(parts2[i])
		if err != nil {
			return 0, fmt.Errorf("invalid version number: %s", parts2[i])
		}

		if n1 > n2 {
			return 1, nil
		} else if n1 < n2 {
			return -1, nil
		}
	}

	return 0, nil
}

// IsNewerVersion checks if newVersion is newer than currentVersion
func IsNewerVersion(currentVersion, newVersion string) (bool, error) {
	result, err := CompareVersions(newVersion, currentVersion)
	if err != nil {
		return !sameVersionIdentifier(currentVersion, newVersion), nil
	}
	return result > 0, nil
}

func normalizeSemanticVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	if index := strings.IndexAny(version, "-+"); index >= 0 {
		version = version[:index]
	}
	return version
}

func sameVersionIdentifier(v1, v2 string) bool {
	return strings.TrimSpace(v1) == strings.TrimSpace(v2)
}
