package discovery

import (
	"slices"
	"strings"

	"github.com/danielfnfaria/agtop/internal/config"
)

// SessionKey is "<profile label>:<sessionId>".
func SessionKey(profile config.ProfileDir, sessionID string) string {
	return profile.Label + ":" + sessionID
}

// DiscoverLiveSessions reads the registry of each profile, locates the files of each session
// and splits live from dead. Live has at most one entry per sessionId (alive pid with the most
// recent updatedAt); everything else goes to Dead with a reason (dead_pid, pre_boot or
// duplicate). Non-interactive sessions (kind other than interactive) are included.
//
// Cost: each sessionId is located once per call (duplicates reuse it); non-live sessions are
// located once while they stay in the registry (deps.InactiveFilesCache). Both caches are
// pruned by the keys still in the registry.
func DiscoverLiveSessions(profiles []config.ProfileDir, deps Deps) Result {
	result := Result{ScannedAt: deps.Now()}
	for _, profile := range profiles {
		reg := ReadSessionRegistry(profile, deps)
		ordered := slices.Clone(reg.Records)
		slices.SortStableFunc(ordered, compareRecords)

		pd := ProfileDiscovery{Profile: profile, Diagnostics: reg.Diagnostics}
		liveIDs := map[string]bool{}
		filesByID := map[string]SessionFiles{}
		inactiveKeys := map[string]bool{}
		registryKeys := map[string]bool{}
		for _, record := range ordered {
			duplicate := record.Alive && liveIDs[record.SessionID]
			inactive := !record.Alive
			cacheKey := LocateCacheKey(profile.Dir, record.SessionID)
			registryKeys[cacheKey] = true
			if inactive {
				inactiveKeys[cacheKey] = true
			}

			files, ok := filesByID[record.SessionID]
			if !ok && inactive {
				files, ok = deps.InactiveFilesCache.Get(cacheKey)
			}
			if !ok {
				files = LocateSessionFiles(profile, record.SessionID, deps, LocateOptions{Cwd: record.Cwd})
				if inactive {
					deps.InactiveFilesCache.Set(cacheKey, files)
				}
			}
			filesByID[record.SessionID] = files

			session := DiscoveredSession{
				LiveSessionRecord: record,
				Key:               SessionKey(profile, record.SessionID),
				Files:             files,
			}
			switch {
			case record.Alive && !duplicate:
				liveIDs[record.SessionID] = true
				pd.Live = append(pd.Live, session)
			case duplicate:
				pd.Dead = append(pd.Dead, InactiveSession{session, InactiveDuplicate})
			default:
				pd.Dead = append(pd.Dead, InactiveSession{session, InactiveReason(record.DeadReason)})
			}
		}
		pruneCache(deps.InactiveFilesCache, profile, inactiveKeys)
		pruneCache(deps.LocateCache, profile, registryKeys)
		result.Profiles = append(result.Profiles, pd)
	}
	return result
}

// pruneCache removes the entries of profile that are not in keep: from the inactive-files cache
// those no longer dead in the registry; from the locate cache those that left the registry.
func pruneCache[V any](cache *Cache[V], profile config.ProfileDir, keep map[string]bool) {
	prefix := LocateCacheKey(profile.Dir, "")
	for _, key := range cache.Keys() {
		if strings.HasPrefix(key, prefix) && !keep[key] {
			cache.Delete(key)
		}
	}
}

// compareRecords puts alive first, then the most recent updatedAt, then the lowest pid.
func compareRecords(a, b LiveSessionRecord) int {
	switch {
	case a.Alive != b.Alive:
		if a.Alive {
			return -1
		}
		return 1
	case a.UpdatedAt != b.UpdatedAt:
		if a.UpdatedAt > b.UpdatedAt {
			return -1
		}
		return 1
	}
	return a.PID - b.PID
}
