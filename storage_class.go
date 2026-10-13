package gofakes3

import (
	"fmt"
	"strings"
	"time"
)

const (
	metaStorageClass       = "X-Amz-Storage-Class"
	metaRestoreAvailableAt = "X-Gofakes3-Restore-Available-At"
	metaRestoreExpiry      = "X-Gofakes3-Restore-Expiry"
	internalMetaPrefix     = "X-Gofakes3-"
)

type restorePhase int

const (
	restoreNone restorePhase = iota
	restoreOngoing
	restoreAvailable
)

type objectRestoreStatus struct {
	phase       restorePhase
	availableAt time.Time
	expiry      time.Time
}

func isValidStorageClass(sc StorageClass) bool {
	switch sc {
	case StorageStandard,
		StorageStandardIA,
		StorageReducedRedundancy,
		StorageOneZoneIA,
		StorageIntelligentTiering,
		StorageGlacier,
		StorageDeepArchive,
		StorageGlacierIR:
		return true
	default:
		return false
	}
}

// isArchivedStorageClass reports whether the storage class requires a
// RestoreObject temporary copy before Get/Copy can read object data.
func isArchivedStorageClass(sc StorageClass) bool {
	switch sc {
	case StorageGlacier, StorageDeepArchive:
		return true
	default:
		return false
	}
}

// StorageClassFromMetadata returns the object's storage class from metadata,
// defaulting to STANDARD when unset.
func StorageClassFromMetadata(meta map[string]string) StorageClass {
	if meta == nil {
		return StorageStandard
	}
	sc := StorageClass(meta[metaStorageClass])
	if sc == "" {
		return StorageStandard
	}
	return sc
}

// ensureStorageClass validates X-Amz-Storage-Class in meta and defaults it to
// STANDARD when missing.
func ensureStorageClass(meta map[string]string) error {
	if meta == nil {
		return nil
	}
	sc := meta[metaStorageClass]
	if sc == "" {
		meta[metaStorageClass] = string(StorageStandard)
		return nil
	}
	if !isValidStorageClass(StorageClass(sc)) {
		return ErrInvalidStorageClass
	}
	return nil
}

func validateRestoreDays(days, maxDays int) error {
	if maxDays < RestoreDaysMin {
		maxDays = RestoreDaysMax
	}
	if days < RestoreDaysMin || days > maxDays {
		return ErrorInvalidArgument(
			"Days",
			fmt.Sprintf("%d", days),
			fmt.Sprintf("Restore Days must be between %d and %d inclusive", RestoreDaysMin, maxDays),
		)
	}
	return nil
}

func parseRestoreTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t, true
}

func restoreStatus(now time.Time, meta map[string]string) objectRestoreStatus {
	if meta == nil {
		return objectRestoreStatus{phase: restoreNone}
	}
	availableAt, okAvail := parseRestoreTime(meta[metaRestoreAvailableAt])
	expiry, okExpiry := parseRestoreTime(meta[metaRestoreExpiry])
	if !okAvail || !okExpiry {
		return objectRestoreStatus{phase: restoreNone}
	}
	st := objectRestoreStatus{availableAt: availableAt, expiry: expiry}
	if now.Before(availableAt) {
		st.phase = restoreOngoing
		return st
	}
	if !now.Before(expiry) {
		st.phase = restoreNone
		return st
	}
	st.phase = restoreAvailable
	return st
}

// archiveReadable reports whether Get/Copy may read object bytes for the
// storage class and restore state in meta.
func archiveReadable(now time.Time, meta map[string]string) bool {
	sc := StorageClassFromMetadata(meta)
	if !isArchivedStorageClass(sc) {
		return true
	}
	return restoreStatus(now, meta).phase == restoreAvailable
}

// glacierReadable is kept as an alias for archiveReadable for call sites that
// historically referred only to GLACIER.
func glacierReadable(now time.Time, meta map[string]string) bool {
	return archiveReadable(now, meta)
}

func formatAmzRestoreHeader(st objectRestoreStatus) string {
	switch st.phase {
	case restoreOngoing:
		return `ongoing-request="true"`
	case restoreAvailable:
		// AWS uses RFC1123-like dates in GMT for expiry-date.
		return fmt.Sprintf(`ongoing-request="false", expiry-date="%s"`, formatHeaderTime(st.expiry))
	default:
		return ""
	}
}

func isInternalMetaKey(k string) bool {
	return strings.HasPrefix(k, internalMetaPrefix)
}

func setRestoreMetadata(meta map[string]string, availableAt, expiry time.Time) {
	meta[metaRestoreAvailableAt] = availableAt.UTC().Format(time.RFC3339Nano)
	meta[metaRestoreExpiry] = expiry.UTC().Format(time.RFC3339Nano)
}

func restoreTierDelay(base time.Duration, tier string) (time.Duration, error) {
	switch tier {
	case "", RestoreTierStandard:
		return base, nil
	case RestoreTierExpedited:
		return 0, nil
	case RestoreTierBulk:
		return base * 2, nil
	default:
		return 0, ErrorInvalidArgument(
			"Tier",
			tier,
			"GlacierJobParameters Tier must be Expedited, Standard, or Bulk",
		)
	}
}

func renewRestoreExpiry(mode RestoreRenewalMode, now, currentExpiry time.Time, days int) time.Time {
	delta := time.Duration(days) * 24 * time.Hour
	switch mode {
	case RestoreRenewalFromNow:
		return now.Add(delta)
	default:
		return currentExpiry.Add(delta)
	}
}
