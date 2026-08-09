package calendarsync

import (
	"hash/fnv"

	"github.com/google/uuid"
)

// projectColorPalette is a fixed set of visually distinct, reasonably
// saturated colors (as 8-hex-digit RGBA, matching Apple's calendar-color
// WebDAV property convention) picked for contrast against each other and
// against typical Calendar app backgrounds. project.Project has no color
// field of its own, so colors are derived deterministically from the
// project ID instead of requiring users to pick one.
var projectColorPalette = []string{
	"#4A90D9FF", // blue
	"#D94A4AFF", // red
	"#4AD97AFF", // green
	"#D9A64AFF", // orange
	"#9B4AD9FF", // purple
	"#4AD9D0FF", // teal
	"#D94A9EFF", // pink
	"#8CD94AFF", // lime
	"#4A5FD9FF", // indigo
	"#D9C34AFF", // gold
}

// ProjectColor deterministically maps a project ID to one of
// projectColorPalette's entries. Same ID always yields the same color
// (stable across process restarts, unlike e.g. a random pick per boot), and
// different IDs are spread roughly evenly across the palette via FNV-1a.
func ProjectColor(projectID uuid.UUID) string {
	h := fnv.New32a()
	_, _ = h.Write(projectID[:])
	return projectColorPalette[h.Sum32()%uint32(len(projectColorPalette))]
}
