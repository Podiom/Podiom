package marketplace

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// defaultMaxSkillBytes caps a downloaded skill (FR-14, default 50 MB).
	defaultMaxSkillBytes int64 = 50 * 1024 * 1024
	// defaultSearchTTLMinutes / defaultDetailTTLHours are the SRC-4 cache windows.
	defaultSearchTTLMinutes = 15
	defaultDetailTTLHours   = 24

	// envSkillsMPKey is the environment override for the SkillsMP API key. It is a
	// SECRET and must never reach the frontend (API-2 / NFR-5).
	envSkillsMPKey = "PODIOM_SKILLSMP_API_KEY"
	// skillsMPKeyFile is the 0600 on-disk fallback under MarketplaceDir, mirroring
	// github/token.json.
	skillsMPKeyFile = "skillsmp.key"
)

// loadSkillsMPKey resolves the SkillsMP API key from the environment first, then
// the 0600 file under marketplaceDir. An empty result means anonymous access,
// which must keep working. The key is never returned to callers outside the
// backend and never logged.
func loadSkillsMPKey(marketplaceDir string) string {
	if v := strings.TrimSpace(os.Getenv(envSkillsMPKey)); v != "" {
		return v
	}
	if marketplaceDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(marketplaceDir, skillsMPKeyFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
