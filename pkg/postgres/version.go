package postgres

import (
	"context"
	"fmt"
)

// Version identifies a PostgreSQL server release.
type Version struct {
	Major int
	Minor int
	Patch int
}

// MajorVersionRange is an inclusive range of supported PostgreSQL major versions.
type MajorVersionRange struct {
	Minimum int
	Maximum int
}

// Contains reports whether version's major release is within the range.
func (r MajorVersionRange) Contains(version Version) bool {
	return version.Major >= r.Minimum && version.Major <= r.Maximum
}

func (v Version) String() string {
	if v.Major >= 10 {
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}

	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ParseServerVersionNum converts PostgreSQL's server_version_num encoding to Version.
func ParseServerVersionNum(version int) Version {
	if version >= 100000 {
		return Version{Major: version / 10000, Minor: version % 100}
	}

	return Version{
		Major: version / 10000,
		Minor: (version / 100) % 100,
		Patch: version % 100,
	}
}

// ServerVersion reads and parses PostgreSQL's numeric server version.
func ServerVersion(ctx context.Context, c Client) (Version, error) {
	var version int
	row, err := c.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer")
	if err != nil {
		return Version{}, fmt.Errorf("creating PostgreSQL server version query: %w", err)
	}
	if err := row.Scan(&version); err != nil {
		return Version{}, fmt.Errorf("reading PostgreSQL server version: %w", err)
	}

	return ParseServerVersionNum(version), nil
}
