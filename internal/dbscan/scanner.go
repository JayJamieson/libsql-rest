// Package dbscan scans database/sql rows into column-keyed maps. It lives in
// its own package (rather than alongside the driver setup) so it can be reused
// by the store and tests without importing database drivers.
package dbscan

// ColScanner is the subset of *sql.Rows needed to scan a single row into a map.
type ColScanner interface {
	Columns() ([]string, error)
	Scan(dest ...any) error
	Err() error
}

// MapScan scans the current row of r into dest, keyed by column name.
func MapScan(r ColScanner, dest map[string]any) error {
	columns, err := r.Columns()
	if err != nil {
		return err
	}

	values := make([]any, len(columns))
	for i := range values {
		values[i] = new(any)
	}

	if err := r.Scan(values...); err != nil {
		return err
	}

	for i, column := range columns {
		dest[column] = *(values[i].(*any))
	}

	return r.Err()
}
